package cinc

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// CookbookVersion is one version entry in a cookbook list.
type CookbookVersion struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// CookbookListEntry is the list response value for one cookbook name.
type CookbookListEntry struct {
	URL      string            `json:"url"`
	Versions []CookbookVersion `json:"versions"`
}

// CookbookFileRef is one file reference in a cookbook version manifest.
type CookbookFileRef struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Specificity string `json:"specificity"`
	Checksum    string `json:"checksum"`
	URL         string `json:"url"`
}

// CookbookMetadata is the cookbook's metadata block, as compiled from its
// metadata.rb/metadata.json and returned inside a cookbook version manifest.
// Every field is optional — older cookbooks and trimmed server responses may
// populate only some of them.
type CookbookMetadata struct {
	Name            string `json:"name,omitempty"`
	Version         string `json:"version,omitempty"`
	Description     string `json:"description,omitempty"`
	LongDescription string `json:"long_description,omitempty"`
	Maintainer      string `json:"maintainer,omitempty"`
	MaintainerEmail string `json:"maintainer_email,omitempty"`
	License         string `json:"license,omitempty"`
	SourceURL       string `json:"source_url,omitempty"`
	IssuesURL       string `json:"issues_url,omitempty"`
	Privacy         bool   `json:"privacy,omitempty"`

	// Constraint maps: key -> version constraint string (e.g. ">= 1.0.0").
	Dependencies map[string]string `json:"dependencies,omitempty"`
	Platforms    map[string]string `json:"platforms,omitempty"`
	Providing    map[string]string `json:"providing,omitempty"`
	// Recipes maps a recipe name to its human description.
	Recipes map[string]string `json:"recipes,omitempty"`

	// Attributes and Groupings are free-form nested structures kept as-is so
	// no information is lost; their inner shape is cookbook-defined.
	Attributes map[string]any `json:"attributes,omitempty"`
	Groupings  map[string]any `json:"groupings,omitempty"`

	// Version-constraint lists, each an array of constraint tokens
	// (e.g. [[">= 13.0", "< 19.0"]] for chef_versions).
	ChefVersions [][]string `json:"chef_versions,omitempty"`
	OhaiVersions [][]string `json:"ohai_versions,omitempty"`
	Gems         [][]string `json:"gems,omitempty"`
}

// Cookbook is a single cookbook version's manifest as returned by the server.
// A server returns files in one of two shapes: the per-segment slices (the
// classic cookbook_version layout) or the flat AllFilesManifest ("all_files",
// used by Policyfile-era cookbook and cookbook_artifact manifests). AllFiles
// merges whichever the server populated.
type Cookbook struct {
	CookbookName string `json:"cookbook_name"`
	Name         string `json:"name"`
	Version      string `json:"version"`

	// Metadata is the cookbook's compiled metadata (description, maintainer,
	// license, dependencies, …), returned by the server alongside the file
	// manifest. omitzero keeps an empty block out of any re-encoded manifest,
	// since omitempty does not elide a zero-valued nested struct.
	Metadata CookbookMetadata `json:"metadata,omitzero"`

	// AllFilesManifest is the flat "all_files" file list. Cookbooks uploaded by
	// this client use it, and modern servers return it on Get.
	AllFilesManifest []CookbookFileRef `json:"all_files,omitempty"`

	// Per-segment slices (classic cookbook_version layout).
	Files       []CookbookFileRef `json:"files"`
	Definitions []CookbookFileRef `json:"definitions"`
	Libraries   []CookbookFileRef `json:"libraries"`
	Attributes  []CookbookFileRef `json:"attributes"`
	Recipes     []CookbookFileRef `json:"recipes"`
	Providers   []CookbookFileRef `json:"providers"`
	Resources   []CookbookFileRef `json:"resources"`
	RootFiles   []CookbookFileRef `json:"root_files"`
	Templates   []CookbookFileRef `json:"templates"`
}

// AllFiles flattens the flat all_files manifest and all nine per-segment slices
// into a single slice, deduplicated by path.
//
// Most servers populate one shape or the other, but nothing guarantees it, and
// a file listed in both must still yield one entry: callers turn each entry
// into work (Download turns it into a file to fetch and write), so a duplicate
// means two writers for one path. The all_files entry wins, and within the
// per-segment slices the first occurrence does.
func (cb *Cookbook) AllFiles() []CookbookFileRef {
	total := len(cb.AllFilesManifest) +
		len(cb.Files) + len(cb.Definitions) + len(cb.Libraries) +
		len(cb.Attributes) + len(cb.Recipes) + len(cb.Providers) +
		len(cb.Resources) + len(cb.RootFiles) + len(cb.Templates)
	all := make([]CookbookFileRef, 0, total)
	seen := make(map[string]bool, total)
	add := func(refs []CookbookFileRef) {
		for _, ref := range refs {
			if seen[ref.Path] {
				continue
			}
			seen[ref.Path] = true
			all = append(all, ref)
		}
	}
	add(cb.AllFilesManifest)
	for _, seg := range [][]CookbookFileRef{
		cb.Files, cb.Definitions, cb.Libraries, cb.Attributes,
		cb.Recipes, cb.Providers, cb.Resources, cb.RootFiles, cb.Templates,
	} {
		add(seg)
	}
	return all
}

// cookbookFile is one file belonging to a cookbook being uploaded.
type cookbookFile struct {
	name     string // path relative to the cookbook root, e.g. recipes/default.rb
	content  []byte
	checksum string // hex MD5
}

// LocalCookbook is a cookbook assembled from disk, ready to upload.
// When Identifier is set the manifest is emitted as a cookbook artifact
// version (chef_type "cookbook_artifact_version") rather than a plain version.
type LocalCookbook struct {
	Name       string
	Version    string
	Identifier string // set for cookbook artifact uploads
	files      []cookbookFile
}

// CookbooksService accesses the /cookbooks endpoints.
type CookbooksService struct{ client *Client }

// List returns all cookbooks and their available versions.
func (s *CookbooksService) List(ctx context.Context) (map[string]CookbookListEntry, *Response, error) {
	return do[map[string]CookbookListEntry](ctx, s.client, "GET",
		s.client.orgPath("/cookbooks"), nil)
}

// ListLatest returns the name->URL index of the latest version of each
// cookbook (GET /cookbooks/_latest).
func (s *CookbooksService) ListLatest(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET",
		s.client.orgPath("/cookbooks/_latest"), nil)
}

// ListRecipes returns every recipe in the latest version of each cookbook
// (GET /cookbooks/_recipes).
func (s *CookbooksService) ListRecipes(ctx context.Context) ([]string, *Response, error) {
	return do[[]string](ctx, s.client, "GET",
		s.client.orgPath("/cookbooks/_recipes"), nil)
}

// GetVersions returns the available versions of a single cookbook
// (GET /cookbooks/NAME), unwrapped from the server's single-key
// {name: {url, versions}} envelope. numVersions limits the versions returned
// ("" for the server default of one, "all" for every version, or "n");
// versions come back newest-first.
func (s *CookbooksService) GetVersions(ctx context.Context, name, numVersions string) (*CookbookListEntry, *Response, error) {
	path := s.client.orgPath("/cookbooks/" + name)
	if numVersions != "" {
		path += "?num_versions=" + url.QueryEscape(numVersions)
	}
	m, resp, err := do[map[string]CookbookListEntry](ctx, s.client, "GET", path, nil)
	if err != nil {
		return nil, resp, err
	}
	entry, ok := m[name]
	if !ok {
		return nil, resp, fmt.Errorf("cinc: cookbook %q missing from response", name)
	}
	return &entry, resp, nil
}

// Get retrieves a single cookbook version manifest.
func (s *CookbooksService) Get(ctx context.Context, name, version string) (*Cookbook, *Response, error) {
	cb, resp, err := do[Cookbook](ctx, s.client, "GET",
		s.client.orgPath("/cookbooks/"+name+"/"+version), nil)
	return ptrOrNil(cb, err), resp, err
}

// Delete removes a single cookbook version.
func (s *CookbooksService) Delete(ctx context.Context, name, version string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/cookbooks/"+name+"/"+version), nil)
	return resp, err
}

// Upload uploads a LocalCookbook: sandbox -> file PUTs -> version manifest PUT.
func (s *CookbooksService) Upload(ctx context.Context, cb *LocalCookbook) error {
	return uploadCookbook(ctx, s.client, "/cookbooks", cb)
}

// Download fetches a cookbook version manifest and writes every file in all
// nine segments to destDir, recreating the path hierarchy. version may be the
// literal string "_latest". File content is fetched from pre-signed bookshelf
// URLs using a plain (unsigned) HTTP GET, matching the upload path in sandboxes.go.
func (s *CookbooksService) Download(ctx context.Context, name, version, destDir string) error {
	cb, _, err := s.Get(ctx, name, version)
	if err != nil {
		return fmt.Errorf("cinc: get cookbook manifest: %w", err)
	}
	// Resolve and validate every destination path up front (cheap, sequential)
	// so a traversal attempt fails fast before any file is fetched or written.
	refs := cb.AllFiles()
	type fileDownload struct{ url, dest, path string }
	jobs := make([]fileDownload, len(refs))
	for i, ref := range refs {
		dest := filepath.Join(destDir, filepath.FromSlash(ref.Path))
		rel, err := filepath.Rel(destDir, dest)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("cinc: unsafe file path in cookbook manifest: %q", ref.Path)
		}
		jobs[i] = fileDownload{url: ref.URL, dest: dest, path: ref.Path}
	}
	// Cookbook files are independent and latency-bound; fetch them in parallel.
	return parallelForEach(ctx, jobs, func(ctx context.Context, j fileDownload) error {
		if err := s.client.downloadFile(ctx, j.url, j.dest); err != nil {
			return fmt.Errorf("cinc: download %s: %w", j.path, err)
		}
		return nil
	})
}

// downloadFile GETs a pre-signed bookshelf URL (no Chef signing) and writes
// the body to dest, creating parent directories as needed.
func (c *Client) downloadFile(ctx context.Context, fileURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return fmt.Errorf("cinc: build download request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cinc: fetch file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return newErrorResponse("GET", fileURL, resp.StatusCode, body)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cinc: read file body: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("cinc: create dirs: %w", err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("cinc: write file: %w", err)
	}
	return nil
}

// uploadCookbook implements the three-step upload, shared with cookbook_artifacts.
func uploadCookbook(ctx context.Context, c *Client, base string, cb *LocalCookbook) error {
	hexes := make([]string, 0, len(cb.files))
	for _, f := range cb.files {
		hexes = append(hexes, f.checksum)
	}
	sb, _, err := c.createSandbox(ctx, hexes)
	if err != nil {
		return fmt.Errorf("cinc: create sandbox: %w", err)
	}
	// Collect the files the server actually needs, deduped by checksum so two
	// files with identical content don't both PUT to the same pre-signed URL.
	type uploadJob struct {
		url, name string
		content   []byte
	}
	seen := make(map[string]bool, len(cb.files))
	var jobs []uploadJob
	for _, f := range cb.files {
		entry, needed := sb.Checksums[f.checksum]
		if !needed || !entry.NeedsUpload || seen[f.checksum] {
			continue
		}
		seen[f.checksum] = true
		jobs = append(jobs, uploadJob{url: entry.URL, name: f.name, content: f.content})
	}
	// Uploads are independent and latency-bound; run them in parallel.
	if err := parallelForEach(ctx, jobs, func(ctx context.Context, j uploadJob) error {
		if err := c.uploadFile(ctx, j.url, j.content); err != nil {
			return fmt.Errorf("cinc: upload %s: %w", j.name, err)
		}
		return nil
	}); err != nil {
		return err
	}
	if _, err := c.commitSandbox(ctx, sb.ID); err != nil {
		return fmt.Errorf("cinc: commit sandbox: %w", err)
	}
	manifest := cookbookManifest(cb)
	// Use Identifier for artifact uploads; Version for regular cookbooks.
	slug := cb.Version
	if cb.Identifier != "" {
		slug = cb.Identifier
	}
	_, _, err = do[map[string]any](ctx, c, "PUT",
		c.orgPath(base+"/"+cb.Name+"/"+slug), manifest)
	if err != nil {
		return fmt.Errorf("cinc: put cookbook manifest: %w", err)
	}
	return nil
}

// cookbookManifest builds the version manifest body for an upload.
// When cb.Identifier is set it emits a cookbook artifact manifest
// (chef_type "cookbook_artifact_version"); otherwise a plain version manifest.
func cookbookManifest(cb *LocalCookbook) map[string]any {
	all := make([]map[string]any, 0, len(cb.files))
	for _, f := range cb.files {
		all = append(all, map[string]any{
			"name": filepath.Base(f.name), "path": f.name,
			"checksum": f.checksum, "specificity": "default",
		})
	}
	if cb.Identifier != "" {
		return map[string]any{
			"cookbook_name": cb.Name,
			"name":          cb.Name,
			"identifier":    cb.Identifier,
			"all_files":     all,
			"chef_type":     "cookbook_artifact_version",
		}
	}
	return map[string]any{
		"cookbook_name": cb.Name,
		"name":          cb.Name + "-" + cb.Version,
		"version":       cb.Version,
		"all_files":     all,
		"chef_type":     "cookbook_version",
	}
}

// LocalCookbookFromDir walks a cookbook directory into a LocalCookbook ready to
// pass to CookbooksService.Upload (or, with an identifier,
// CookbookArtifactsService.Upload). The cookbook name is taken from the base
// name of dir. Every regular file under dir is read and checksummed, except
// those excluded by a chefignore file at the cookbook root — matching knife, an
// uploaded cookbook omits chefignored files. An empty directory is an error.
func LocalCookbookFromDir(dir, version string) (*LocalCookbook, error) {
	cb := &LocalCookbook{Name: filepath.Base(dir), Version: version}
	ignore, err := LoadChefignore(dir)
	if err != nil {
		return nil, fmt.Errorf("cinc: read chefignore: %w", err)
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// Prune whole subtrees a chefignore pattern excludes (e.g. a bare
			// directory name like `.kitchen`), so their files are never read.
			if rel != "." && ignore.Ignores(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if ignore.Ignores(rel) {
			return nil
		}
		// Only regular files belong in a cookbook. WalkDir does not follow
		// symlinks but os.ReadFile does, so without this an entry symlinked
		// out of the cookbook would be uploaded with its target's content,
		// and a dangling one would abort the whole walk.
		if !d.Type().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		cb.files = append(cb.files, cookbookFile{
			name: rel, content: content, checksum: md5Hex(content),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cinc: read cookbook dir: %w", err)
	}
	if len(cb.files) == 0 {
		return nil, fmt.Errorf("cinc: no files found in %s", dir)
	}
	return cb, nil
}
