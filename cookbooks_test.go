package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestCookbooks_GetVersions(t *testing.T) {
	t.Run("unwraps single-key envelope and sends num_versions", func(t *testing.T) {
		srv := cinctest.New(t)
		var gotNumVersions string
		srv.Handle("GET /organizations/o/cookbooks/apache2", cinctest.Route{
			Body: `{"apache2":{"url":"http://x/apache2","versions":[` +
				`{"url":"http://x/apache2/5.1.0","version":"5.1.0"},` +
				`{"url":"http://x/apache2/4.2.0","version":"4.2.0"}]}}`,
			Assert: func(t *testing.T, r *http.Request, _ []byte) {
				gotNumVersions = r.URL.Query().Get("num_versions")
			},
		})
		c := newTestClient(t, srv.Server)

		entry, resp, err := c.Cookbooks.GetVersions(context.Background(), "apache2", "all")
		if err != nil {
			t.Fatalf("GetVersions: %v", err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if gotNumVersions != "all" {
			t.Errorf("num_versions = %q, want all", gotNumVersions)
		}
		if entry == nil || len(entry.Versions) != 2 || entry.Versions[0].Version != "5.1.0" {
			t.Fatalf("entry = %+v, want 2 versions newest-first", entry)
		}
	})

	t.Run("omits num_versions when empty", func(t *testing.T) {
		srv := cinctest.New(t)
		var hadParam bool
		srv.Handle("GET /organizations/o/cookbooks/nginx", cinctest.Route{
			Body: `{"nginx":{"url":"http://x/nginx","versions":[{"url":"http://x/nginx/1.0.0","version":"1.0.0"}]}}`,
			Assert: func(t *testing.T, r *http.Request, _ []byte) {
				_, hadParam = r.URL.Query()["num_versions"]
			},
		})
		c := newTestClient(t, srv.Server)

		if _, _, err := c.Cookbooks.GetVersions(context.Background(), "nginx", ""); err != nil {
			t.Fatalf("GetVersions: %v", err)
		}
		if hadParam {
			t.Error("num_versions should be absent when numVersions is empty")
		}
	})

	t.Run("propagates 404", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/cookbooks/missing", cinctest.Route{
			Status: 404, Body: `{"error":["not found"]}`,
		})
		c := newTestClient(t, srv.Server)

		entry, _, err := c.Cookbooks.GetVersions(context.Background(), "missing", "")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if entry != nil {
			t.Errorf("entry = %+v, want nil on error", entry)
		}
	})
}

func TestCookbooks_GetAndList(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks",
		cinctest.Route{Body: `{"nginx":{"url":"http://x/cookbooks/nginx","versions":[{"version":"1.2.0","url":"http://x/cookbooks/nginx/1.2.0"}]}}`})
	srv.Handle("GET /organizations/o/cookbooks/nginx/1.2.0",
		cinctest.Route{Body: `{"cookbook_name":"nginx","version":"1.2.0","name":"nginx-1.2.0"}`})
	srv.Handle("DELETE /organizations/o/cookbooks/nginx/1.2.0",
		cinctest.Route{Body: `{}`})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	list, _, err := c.Cookbooks.List(ctx)
	if err != nil || len(list["nginx"].Versions) != 1 {
		t.Fatalf("List: %+v %v", list, err)
	}
	cb, _, err := c.Cookbooks.Get(ctx, "nginx", "1.2.0")
	if err != nil || cb.Version != "1.2.0" {
		t.Fatalf("Get: %+v %v", cb, err)
	}
	if _, err := c.Cookbooks.Delete(ctx, "nginx", "1.2.0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// Get parses the cookbook version's metadata object — the description,
// maintainer, license, and dependency information the server returns in the
// same response as the file manifest.
func TestCookbooks_GetParsesMetadata(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/apache2/8.6.0",
		cinctest.Route{Body: `{
			"cookbook_name": "apache2",
			"name": "apache2-8.6.0",
			"version": "8.6.0",
			"metadata": {
				"name": "apache2",
				"version": "8.6.0",
				"description": "Installs and configures apache2",
				"long_description": "A longer story about apache2.",
				"maintainer": "Sous Chefs",
				"maintainer_email": "help@sous-chefs.org",
				"license": "Apache-2.0",
				"source_url": "https://github.com/sous-chefs/apache2",
				"issues_url": "https://github.com/sous-chefs/apache2/issues",
				"privacy": false,
				"dependencies": {"logrotate": ">= 0.0.0", "iptables": ">= 1.0"},
				"platforms": {"redhat": ">= 0.0.0"},
				"providing": {"apache2": ">= 0.0.0"},
				"recipes": {"apache2::default": "Installs apache2"},
				"chef_versions": [[">= 13.0"]],
				"ohai_versions": [],
				"gems": []
			}
		}`})

	c := newTestClient(t, srv.Server)
	cb, _, err := c.Cookbooks.Get(context.Background(), "apache2", "8.6.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	m := cb.Metadata
	if m.Description != "Installs and configures apache2" {
		t.Errorf("Description = %q", m.Description)
	}
	if m.LongDescription != "A longer story about apache2." {
		t.Errorf("LongDescription = %q", m.LongDescription)
	}
	if m.Maintainer != "Sous Chefs" || m.MaintainerEmail != "help@sous-chefs.org" {
		t.Errorf("maintainer = %q <%q>", m.Maintainer, m.MaintainerEmail)
	}
	if m.License != "Apache-2.0" {
		t.Errorf("License = %q", m.License)
	}
	if m.SourceURL != "https://github.com/sous-chefs/apache2" {
		t.Errorf("SourceURL = %q", m.SourceURL)
	}
	if m.IssuesURL != "https://github.com/sous-chefs/apache2/issues" {
		t.Errorf("IssuesURL = %q", m.IssuesURL)
	}
	if m.Dependencies["logrotate"] != ">= 0.0.0" || m.Dependencies["iptables"] != ">= 1.0" {
		t.Errorf("Dependencies = %+v", m.Dependencies)
	}
	if m.Platforms["redhat"] != ">= 0.0.0" {
		t.Errorf("Platforms = %+v", m.Platforms)
	}
	if m.Providing["apache2"] != ">= 0.0.0" {
		t.Errorf("Providing = %+v", m.Providing)
	}
	if m.Recipes["apache2::default"] != "Installs apache2" {
		t.Errorf("Recipes = %+v", m.Recipes)
	}
	if len(m.ChefVersions) != 1 || len(m.ChefVersions[0]) != 1 || m.ChefVersions[0][0] != ">= 13.0" {
		t.Errorf("ChefVersions = %+v", m.ChefVersions)
	}
}

// A server (or shape) that omits the metadata object leaves Metadata as its
// zero value rather than failing the decode.
func TestCookbooks_GetWithoutMetadata(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/bare/1.0.0",
		cinctest.Route{Body: `{"cookbook_name":"bare","name":"bare-1.0.0","version":"1.0.0"}`})

	c := newTestClient(t, srv.Server)
	cb, _, err := c.Cookbooks.Get(context.Background(), "bare", "1.0.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cb.Metadata.Description != "" || len(cb.Metadata.Dependencies) != 0 {
		t.Errorf("expected zero-value metadata, got %+v", cb.Metadata)
	}
}

func TestCookbooks_Download(t *testing.T) {
	// The fake server doubles as: (a) the Chef API endpoint that returns the
	// manifest, and (b) the "bookshelf" that serves the pre-signed file URLs.
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbooks/nginx/1.2.0":
			// Chef-signed request — must carry auth header.
			if r.Header.Get("X-Ops-Authorization-1") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			baseURL := "http://" + r.Host
			manifest := fmt.Sprintf(`{
				"cookbook_name":"nginx","name":"nginx-1.2.0","version":"1.2.0",
				"recipes":[{"name":"default.rb","path":"recipes/default.rb","specificity":"default","checksum":"abc","url":"%s/files/recipes/default.rb"}],
				"root_files":[{"name":"metadata.rb","path":"metadata.rb","specificity":"default","checksum":"def","url":"%s/files/metadata.rb"}]
			}`, baseURL, baseURL)
			w.Write([]byte(manifest))
		case r.Method == "GET" && r.URL.Path == "/files/recipes/default.rb":
			// Bookshelf: pre-signed URL — must NOT carry Chef signing header.
			if r.Header.Get("X-Ops-Authorization-1") != "" {
				t.Errorf("bookshelf GET /files/recipes/default.rb carried Chef signing header (should be unsigned)")
			}
			w.Write([]byte("package 'nginx'\n"))
		case r.Method == "GET" && r.URL.Path == "/files/metadata.rb":
			// Bookshelf: pre-signed URL — must NOT carry Chef signing header.
			if r.Header.Get("X-Ops-Authorization-1") != "" {
				t.Errorf("bookshelf GET /files/metadata.rb carried Chef signing header (should be unsigned)")
			}
			w.Write([]byte("name 'nginx'\nversion '1.2.0'\n"))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
		}
	})

	c := newTestClient(t, srv.Server)
	dest := t.TempDir()
	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.2.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}

	recipe := filepath.Join(dest, "recipes", "default.rb")
	if data, err := os.ReadFile(recipe); err != nil || string(data) != "package 'nginx'\n" {
		t.Fatalf("recipe file: data=%q err=%v", data, err)
	}
	metadata := filepath.Join(dest, "metadata.rb")
	if data, err := os.ReadFile(metadata); err != nil || string(data) != "name 'nginx'\nversion '1.2.0'\n" {
		t.Fatalf("metadata file: data=%q err=%v", data, err)
	}
}

func TestCookbooks_Download_PathTraversal(t *testing.T) {
	// A malicious server returns a manifest with a path that escapes destDir.
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbooks/evil/1.0.0":
			if r.Header.Get("X-Ops-Authorization-1") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			baseURL := "http://" + r.Host
			// The path component traverses up out of destDir.
			manifest := fmt.Sprintf(`{
				"cookbook_name":"evil","name":"evil-1.0.0","version":"1.0.0",
				"root_files":[{"name":"escape.txt","path":"../escape.txt","specificity":"default","checksum":"aaa","url":"%s/files/escape.txt"}]
			}`, baseURL)
			w.Write([]byte(manifest))
		case r.Method == "GET" && r.URL.Path == "/files/escape.txt":
			w.Write([]byte("pwned\n"))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
		}
	})

	c := newTestClient(t, srv.Server)
	dest := t.TempDir()
	err := c.Cookbooks.Download(context.Background(), "evil", "1.0.0", dest)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	// The escaped file must NOT have been written.
	escapedPath := filepath.Join(filepath.Dir(dest), "escape.txt")
	if _, statErr := os.Stat(escapedPath); statErr == nil {
		t.Errorf("path traversal: file was written outside destDir at %s", escapedPath)
	}
}

func TestCookbooks_Upload_ParallelDedup(t *testing.T) {
	// A cookbook with three files where two share identical content (and thus
	// the same checksum). The shared checksum must be PUT exactly once, and the
	// concurrent uploads must be race-clean (run with -race).
	dir := t.TempDir()
	root := filepath.Join(dir, "dup")
	os.MkdirAll(filepath.Join(root, "recipes"), 0o755)
	shared := []byte("shared content\n")
	unique := []byte("unique content\n")
	os.WriteFile(filepath.Join(root, "recipes", "a.rb"), shared, 0o644)
	os.WriteFile(filepath.Join(root, "recipes", "b.rb"), shared, 0o644) // same checksum as a.rb
	os.WriteFile(filepath.Join(root, "metadata.rb"), unique, 0o644)

	cb, err := LocalCookbookFromDir(root, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	sharedCk, uniqueCk := md5Hex(shared), md5Hex(unique)

	var mu sync.Mutex
	uploads := map[string]int{} // checksum -> number of PUTs received
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			base := "http://" + r.Host
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"sandbox_id":"sb1","checksums":{
				"%s":{"needs_upload":true,"url":"%s/upload/%s"},
				"%s":{"needs_upload":true,"url":"%s/upload/%s"}}}`,
				sharedCk, base, sharedCk, uniqueCk, base, uniqueCk)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/upload/"):
			ck := strings.TrimPrefix(r.URL.Path, "/upload/")
			mu.Lock()
			uploads[ck]++
			mu.Unlock()
			w.WriteHeader(200)
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb1":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/cookbooks/dup/1.0.0":
			w.WriteHeader(201)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	c := newTestClient(t, srv.Server)
	if err := c.Cookbooks.Upload(context.Background(), cb); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if uploads[sharedCk] != 1 {
		t.Errorf("shared checksum uploaded %d times, want exactly 1", uploads[sharedCk])
	}
	if uploads[uniqueCk] != 1 {
		t.Errorf("unique checksum uploaded %d times, want exactly 1", uploads[uniqueCk])
	}
}

func TestCookbook_AllFiles_ParsesAllFilesManifest(t *testing.T) {
	// A manifest in the flat all_files shape (what this client uploads and what
	// modern servers return) must round-trip through AllFiles().
	var cb Cookbook
	body := `{"cookbook_name":"nginx","name":"nginx-1.0.0","version":"1.0.0",
		"all_files":[
			{"name":"default.rb","path":"recipes/default.rb","checksum":"abc","url":"http://x/abc"},
			{"name":"metadata.rb","path":"metadata.rb","checksum":"def","url":"http://x/def"}]}`
	if err := json.Unmarshal([]byte(body), &cb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	files := cb.AllFiles()
	if len(files) != 2 {
		t.Fatalf("AllFiles() = %d entries, want 2", len(files))
	}
	if files[0].Path != "recipes/default.rb" || files[0].URL != "http://x/abc" {
		t.Errorf("first file = %+v", files[0])
	}
}

func TestLocalCookbookFromDir_ExcludesChefignored(t *testing.T) {
	// A cookbook with a chefignore must omit ignored files from the upload set
	// — previously LocalCookbookFromDir uploaded everything under the directory.
	dir := t.TempDir()
	root := filepath.Join(dir, "nginx")
	os.MkdirAll(filepath.Join(root, "recipes"), 0o755)
	os.MkdirAll(filepath.Join(root, ".kitchen"), 0o755)
	files := map[string]string{
		"metadata.rb":         "name 'nginx'\n",
		"chefignore":          "*.bak\n.kitchen\n",
		"recipes/default.rb":  "package 'nginx'\n",
		"recipes/default.bak": "old\n",
		".kitchen/state.yml":  "state\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cb, err := LocalCookbookFromDir(root, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	var got []string
	for _, f := range cb.files {
		got = append(got, f.name)
	}
	slices.Sort(got)
	want := []string{"chefignore", "metadata.rb", "recipes/default.rb"}
	if !slices.Equal(got, want) {
		t.Fatalf("uploaded files = %v, want %v (chefignored files must be omitted)", got, want)
	}
}

func TestLocalCookbookFromDir_Nonexistent(t *testing.T) {
	_, err := LocalCookbookFromDir(filepath.Join(t.TempDir(), "absent"), "1.0.0")
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestLocalCookbookFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir() // empty cookbook
	_, err := LocalCookbookFromDir(dir, "1.0.0")
	if err == nil {
		t.Fatal("expected error for empty cookbook dir")
	}
	if !contains(err.Error(), "no files found") {
		t.Errorf("error %q should mention no files", err.Error())
	}
}

func TestCookbookManifest_VersionVariant(t *testing.T) {
	cb := &LocalCookbook{Name: "n", Version: "1.0.0", files: []cookbookFile{
		{name: "recipes/default.rb", checksum: "abc"},
	}}
	m := cookbookManifest(cb)
	if m["chef_type"] != "cookbook_version" {
		t.Errorf("chef_type = %v, want cookbook_version", m["chef_type"])
	}
	if m["version"] != "1.0.0" {
		t.Errorf("version = %v, want 1.0.0", m["version"])
	}
	if _, ok := m["identifier"]; ok {
		t.Error("plain version manifest should not include identifier")
	}
	if name, _ := m["name"].(string); name != "n-1.0.0" {
		t.Errorf("name = %v, want n-1.0.0", m["name"])
	}
}

func TestCookbookManifest_ArtifactVariant(t *testing.T) {
	cb := &LocalCookbook{Name: "n", Version: "1.0.0", Identifier: "abc",
		files: []cookbookFile{{name: "recipes/default.rb", checksum: "x"}}}
	m := cookbookManifest(cb)
	if m["chef_type"] != "cookbook_artifact_version" {
		t.Errorf("chef_type = %v", m["chef_type"])
	}
	if m["identifier"] != "abc" {
		t.Errorf("identifier = %v", m["identifier"])
	}
	if _, ok := m["version"]; ok {
		t.Error("artifact manifest should not include a version key")
	}
}

func TestDownloadFile_NotFound(t *testing.T) {
	// A 404 from the bookshelf must surface as an ErrorResponse.
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbooks/nginx/1.2.0":
			if r.Header.Get("X-Ops-Authorization-1") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(w, `{
				"cookbook_name":"nginx","name":"nginx-1.2.0","version":"1.2.0",
				"root_files":[{"name":"metadata.rb","path":"metadata.rb","specificity":"default","checksum":"x","url":"http://%s/files/nope"}]
			}`, r.Host)
		case r.URL.Path == "/files/nope":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":["gone"]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	c := newTestClient(t, srv.Server)
	err := c.Cookbooks.Download(context.Background(), "nginx", "1.2.0", t.TempDir())
	if err == nil {
		t.Fatal("expected error for bookshelf 404")
	}
}

func TestDownloadFile_BadURL(t *testing.T) {
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/organizations/o/cookbooks/nginx/1.2.0" {
			fmt.Fprint(w, `{
				"cookbook_name":"nginx","name":"nginx-1.2.0","version":"1.2.0",
				"root_files":[{"name":"x","path":"x","specificity":"default","checksum":"x","url":"://not-a-url"}]
			}`)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	})
	c := newTestClient(t, srv.Server)
	err := c.Cookbooks.Download(context.Background(), "nginx", "1.2.0", t.TempDir())
	if err == nil {
		t.Fatal("expected error for malformed file URL")
	}
}

func TestUploadFile_NonSuccess(t *testing.T) {
	// A direct uploadFile call whose URL returns 500 should produce an
	// ErrorResponse describing the upload failure.
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":["bookshelf exploded"]}`))
	})
	c := newTestClient(t, srv.Server)
	err := c.uploadFile(context.Background(), srv.Server.URL+"/file", []byte("data"))
	if err == nil {
		t.Fatal("expected error for non-2xx upload")
	}
	if !contains(err.Error(), "500") {
		t.Errorf("error %q should contain status 500", err.Error())
	}
}

func TestUploadFile_BadURL(t *testing.T) {
	c, _ := NewClient(Config{
		ServerURL: "https://h", Org: "o", ClientName: "c", Key: testRSAKey(t),
	})
	if err := c.uploadFile(context.Background(), "://not-a-url", []byte("x")); err == nil {
		t.Fatal("expected error for malformed upload URL")
	}
}

func TestCookbooks_UploadRoundTrip(t *testing.T) {
	// Build a cookbook on disk: metadata.rb + recipes/default.rb.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "nginx", "recipes"), 0o755)
	metadataContent := []byte("name 'nginx'\nversion '1.2.0'\n")
	recipeContent := []byte("package 'nginx'\n")
	os.WriteFile(filepath.Join(dir, "nginx", "metadata.rb"), metadataContent, 0o644)
	os.WriteFile(filepath.Join(dir, "nginx", "recipes", "default.rb"), recipeContent, 0o644)

	cb, err := LocalCookbookFromDir(filepath.Join(dir, "nginx"), "1.2.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	if len(cb.files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(cb.files))
	}

	// Pick the checksum of metadata.rb as the one needing upload.
	metadataChecksum := md5Hex(metadataContent)

	// Stateful fake: sandbox -> file PUT -> manifest PUT.
	srv := cinctest.New(t)
	uploaded := map[string][]byte{}
	var manifestBody []byte
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			// Return metadata.rb as needing upload; recipes/default.rb does not.
			uploadURL := "http://" + r.Host + "/upload/" + metadataChecksum
			w.WriteHeader(201)
			w.Write([]byte(`{"sandbox_id":"sb1","checksums":{"` + metadataChecksum + `":{"needs_upload":true,"url":"` + uploadURL + `"}}}`))
		case r.Method == "PUT" && r.URL.Path == "/upload/"+metadataChecksum:
			// Pre-signed bookshelf upload — must NOT carry Chef signing header.
			if r.Header.Get("X-Ops-Authorization-1") != "" {
				t.Errorf("file upload PUT carried Chef signing header (should be unsigned)")
			}
			body, _ := io.ReadAll(r.Body)
			uploaded[metadataChecksum] = body
			w.WriteHeader(200)
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb1":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/cookbooks/nginx/1.2.0":
			manifestBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	c := newTestClient(t, srv.Server)
	if err := c.Cookbooks.Upload(context.Background(), cb); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Issue 3: verify the file-upload PUT was exercised.
	got, ok := uploaded[metadataChecksum]
	if !ok {
		t.Fatal("expected metadata.rb to be uploaded, but no PUT was received")
	}
	if string(got) != string(metadataContent) {
		t.Errorf("uploaded metadata.rb = %q, want %q", got, metadataContent)
	}

	// Issue 2: verify manifest includes chef_type "cookbook_version".
	if len(manifestBody) == 0 {
		t.Fatal("no manifest body received")
	}
	body := string(manifestBody)
	if !contains(body, "cookbook_version") {
		t.Errorf("manifest missing chef_type=cookbook_version: %s", body)
	}
}

func TestCookbooks_ListLatest(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/_latest", cinctest.Route{
		Body: `{"apache2":"https://x/cookbooks/apache2/5.1.0","nginx":"https://x/cookbooks/nginx/1.0.0"}`,
	})
	c := newTestClient(t, srv.Server)

	latest, _, err := c.Cookbooks.ListLatest(context.Background())
	if err != nil {
		t.Fatalf("ListLatest: %v", err)
	}
	if latest["apache2"] == "" || latest["nginx"] == "" {
		t.Fatalf("ListLatest = %v", latest)
	}
}

func TestCookbooks_ListRecipes(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/_recipes", cinctest.Route{
		Body: `["apache2","apache2::mod_ssl","nginx"]`,
	})
	c := newTestClient(t, srv.Server)

	recipes, _, err := c.Cookbooks.ListRecipes(context.Background())
	if err != nil {
		t.Fatalf("ListRecipes: %v", err)
	}
	if len(recipes) != 3 || recipes[1] != "apache2::mod_ssl" {
		t.Fatalf("ListRecipes = %v", recipes)
	}
}
