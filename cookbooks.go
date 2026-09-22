package cinc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	path := s.client.orgPath("/cookbooks/" + esc(name))
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
		s.client.orgPath("/cookbooks/"+esc(name)+"/"+esc(version)), nil)
	return ptrOrNil(cb, err), resp, err
}

// Delete removes a single cookbook version.
func (s *CookbooksService) Delete(ctx context.Context, name, version string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/cookbooks/"+esc(name)+"/"+esc(version)), nil)
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
// Each file is verified against the manifest's MD5 checksum and written
// atomically; files already in destDir with a matching checksum are skipped.
func (s *CookbooksService) Download(ctx context.Context, name, version, destDir string) error {
	cb, _, err := s.Get(ctx, name, version)
	if err != nil {
		return fmt.Errorf("cinc: get cookbook manifest: %w", err)
	}
	// Resolve and validate every destination path up front (cheap, sequential)
	// so a traversal attempt fails fast before any file is fetched or written.
	refs := cb.AllFiles()
	type fileDownload struct{ url, dest, path, checksum string }
	jobs := make([]fileDownload, len(refs))
	for i, ref := range refs {
		dest := filepath.Join(destDir, filepath.FromSlash(ref.Path))
		rel, err := filepath.Rel(destDir, dest)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("cinc: unsafe file path in cookbook manifest: %q", ref.Path)
		}
		jobs[i] = fileDownload{url: ref.URL, dest: dest, path: ref.Path, checksum: ref.Checksum}
	}
	// Cookbook files are independent and latency-bound; fetch them in parallel.
	return parallelForEach(ctx, jobs, func(ctx context.Context, j fileDownload) error {
		if err := s.client.downloadFile(ctx, j.url, j.dest, j.checksum); err != nil {
			return fmt.Errorf("cinc: download %s: %w", j.path, err)
		}
		return nil
	})
}

// downloadFile GETs a pre-signed bookshelf URL (no Chef signing) and writes
// the body to dest, creating parent directories as needed.
//
// checksum is the hex MD5 the cookbook manifest lists for the file. When set,
// a file already at dest with that digest is left alone and not fetched, and
// a fetched body that does not match it is rejected. The body is streamed to
// a temp file beside dest and renamed into place only once it is complete and
// verified, so a failed download never leaves a truncated or corrupt file at
// dest. An empty checksum disables both the skip and the check.
func (c *Client) downloadFile(ctx context.Context, fileURL, dest, checksum string) error {
	if checksum != "" {
		if have, err := fileMD5Hex(dest); err == nil && strings.EqualFold(have, checksum) {
			return nil
		}
	}
	// Transient failures, including a body cut off mid-stream, are retried
	// (see doTransfer); writeVerified discards the partial temp file first.
	return c.doTransfer(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
		if err != nil {
			return nil, fmt.Errorf("cinc: build download request: %w", err)
		}
		return req, nil
	}, func(resp *http.Response) error {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("cinc: create dirs: %w", err)
		}
		return writeVerified(dest, wireReader{resp.Body}, checksum)
	})
}

// writeVerified streams r into dest atomically: it writes to a temp file in
// dest's directory (so the final rename cannot cross filesystems), hashing as
// it goes, and renames it over dest only if the digest matches checksum (or
// checksum is empty). On any failure the temp file is removed and dest is
// untouched.
func writeVerified(dest string, r io.Reader, checksum string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*.tmp")
	if err != nil {
		return fmt.Errorf("cinc: create temp file: %w", err)
	}
	defer func() {
		if err != nil {
			// Best-effort cleanup; the failure being returned is the one
			// the caller needs to see.
			_ = os.Remove(tmp.Name())
		}
	}()
	h := newMD5()
	_, err = io.Copy(io.MultiWriter(tmp, h), r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("cinc: read file body: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); checksum != "" && !strings.EqualFold(got, checksum) {
		return fmt.Errorf("cinc: checksum mismatch: got md5 %s, manifest lists %s", got, checksum)
	}
	// CreateTemp makes the file 0600; cookbook files are ordinary 0644 files.
	if err = os.Chmod(tmp.Name(), 0o644); err == nil {
		err = os.Rename(tmp.Name(), dest)
	}
	if err != nil {
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
		c.orgPath(base+"/"+esc(cb.Name)+"/"+esc(slug)), manifest)
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
// CookbookArtifactsService.Upload). It selects files the way Chef's
// CookbookVersionLoader does:
//
//   - directories at the cookbook root whose names begin with "." (.git,
//     .kitchen, …) are skipped; dotfiles, and dot-directories deeper down, are
//     kept;
//   - chef-zero's .uploaded-cookbook-version.json is skipped;
//   - files excluded by the applicable chefignore (see LoadChefignore, which
//     also searches parent directories) are skipped.
//
// The cookbook name is the `name` from metadata.json, or else from a literal
// `name '...'` line in metadata.rb, falling back to the base name of dir when
// neither provides one. Every remaining regular file is read and checksummed.
// An empty directory is an error.
func LocalCookbookFromDir(dir, version string) (*LocalCookbook, error) {
	name, err := cookbookNameFromMetadata(dir)
	if err != nil {
		return nil, fmt.Errorf("cinc: read cookbook metadata: %w", err)
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	cb := &LocalCookbook{Name: name, Version: version}
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
			if rel == "." {
				return nil
			}
			// Chef skips dot-directories at the cookbook root, and never needs
			// to descend into a subtree whose every file is chefignored.
			if (!strings.Contains(rel, "/") && strings.HasPrefix(rel, ".")) || ignore.prunes(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == uploadedCookbookVersionFile || ignore.Ignores(rel) {
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

// uploadedCookbookVersionFile is written into cookbooks by chef-zero and is
// never part of the cookbook itself.
const uploadedCookbookVersionFile = ".uploaded-cookbook-version.json"

// metadataRbName matches a `name` call with a string literal in metadata.rb,
// e.g. `name 'nginx'` or `name("nginx")`.
var metadataRbName = regexp.MustCompile(`(?m)^[ \t]*name[ \t]*\(?[ \t]*(?:'([^']*)'|"([^"]*)")`)

// cookbookNameFromMetadata returns the cookbook name declared in dir's
// metadata, or "" when there is none. Like Chef, metadata.json takes
// precedence over metadata.rb. metadata.rb is Ruby and is not evaluated; only
// a `name` call with a literal string argument is recognized.
func cookbookNameFromMetadata(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	switch {
	case err == nil:
		var md struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &md); err != nil {
			return "", fmt.Errorf("metadata.json: %w", err)
		}
		return md.Name, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	data, err = os.ReadFile(filepath.Join(dir, "metadata.rb"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	m := metadataRbName.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	return string(m[1]) + string(m[2]), nil
}
