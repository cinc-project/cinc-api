package cinc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// dlFile is one file a fake bookshelf serves, with the checksum the manifest
// advertises for it (which a test may deliberately get wrong).
type dlFile struct {
	path, body, checksum string
}

// newDownloadFixture serves a cookbook manifest listing files from a fake
// Chef API, and their content from a separate fake bookshelf. The bookshelf
// fails the test if any request carries Chef signing headers, and counts the
// fetches per path. handler, when non-nil, replaces the default body writer.
func newDownloadFixture(t *testing.T, files []dlFile, handler http.HandlerFunc) (*Client, map[string]*int64) {
	t.Helper()
	fetches := make(map[string]*int64, len(files))
	bodies := make(map[string]string, len(files))
	for _, f := range files {
		fetches["/files/"+f.path] = new(int64)
		bodies["/files/"+f.path] = f.body
	}
	shelf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") != "" {
			t.Errorf("bookshelf %s %s carried Chef signing header", r.Method, r.URL.Path)
		}
		n, ok := fetches[r.URL.Path]
		if !ok {
			t.Errorf("unexpected bookshelf request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt64(n, 1)
		if handler != nil {
			handler(w, r)
			return
		}
		fmt.Fprint(w, bodies[r.URL.Path])
	}))
	t.Cleanup(shelf.Close)

	refs := make([]string, 0, len(files))
	for _, f := range files {
		refs = append(refs, fmt.Sprintf(`{"name":%q,"path":%q,"checksum":%q,"url":%q}`,
			filepath.Base(f.path), f.path, f.checksum, shelf.URL+"/files/"+f.path))
	}
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/nginx/1.0.0", cinctest.Route{
		Body: `{"cookbook_name":"nginx","version":"1.0.0","all_files":[` +
			strings.Join(refs, ",") + `]}`,
	})
	return newTestClient(t, srv.Server), fetches
}

// assertNoTempFiles fails if a download left a temp file anywhere under dir.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".tmp") {
			t.Errorf("stray temp file left behind: %s", path)
		}
		return nil
	})
}

const recipeBody = "package 'nginx'\n"

// A body that does not hash to the manifest checksum is rejected, and nothing
// is left at the destination.
func TestDownload_RejectsChecksumMismatch(t *testing.T) {
	c, _ := newDownloadFixture(t, []dlFile{
		{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte("something else"))},
	}, nil)
	dest := t.TempDir()
	err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "recipes", "default.rb")); !os.IsNotExist(statErr) {
		t.Errorf("mismatched file was written to its destination (stat err %v)", statErr)
	}
	assertNoTempFiles(t, dest)
}

// Chef manifests carry lower-case hex, but the comparison should not depend
// on that.
func TestDownload_ChecksumIsCaseInsensitive(t *testing.T) {
	c, _ := newDownloadFixture(t, []dlFile{
		{path: "recipes/default.rb", body: recipeBody, checksum: strings.ToUpper(md5Hex([]byte(recipeBody)))},
	}, nil)
	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", t.TempDir()); err != nil {
		t.Fatalf("Download: %v", err)
	}
}

// Files already on disk with the right content are not fetched again; files
// whose content differs are.
func TestDownload_SkipsUnchangedFiles(t *testing.T) {
	const metaBody = "name 'nginx'\n"
	c, fetches := newDownloadFixture(t, []dlFile{
		{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte(recipeBody))},
		{path: "metadata.rb", body: metaBody, checksum: md5Hex([]byte(metaBody))},
	}, nil)
	dest := t.TempDir()
	writeTestFile(t, filepath.Join(dest, "recipes", "default.rb"), recipeBody)
	writeTestFile(t, filepath.Join(dest, "metadata.rb"), "stale\n")

	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n := atomic.LoadInt64(fetches["/files/recipes/default.rb"]); n != 0 {
		t.Errorf("unchanged file fetched %d times, want 0", n)
	}
	if n := atomic.LoadInt64(fetches["/files/metadata.rb"]); n != 1 {
		t.Errorf("changed file fetched %d times, want 1", n)
	}
	assertFileContent(t, filepath.Join(dest, "metadata.rb"), metaBody)
}

// A transfer that dies mid-body must not clobber what was at the destination
// with a truncated file, nor leave its temp file behind.
func TestDownload_TruncatedBodyLeavesDestinationIntact(t *testing.T) {
	c, _ := newDownloadFixture(t, []dlFile{
		{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte(recipeBody))},
	}, func(w http.ResponseWriter, _ *http.Request) {
		// Promise more than is sent, so the client sees an unexpected EOF.
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("package"))
	})
	dest := t.TempDir()
	target := filepath.Join(dest, "recipes", "default.rb")
	writeTestFile(t, target, "previous content\n")

	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest); err == nil {
		t.Fatal("expected an error from a truncated body")
	}
	assertFileContent(t, target, "previous content\n")
	assertNoTempFiles(t, dest)
}

// Downloaded files are world-readable like any other cookbook file, not the
// 0600 a temp file is created with.
func TestDownload_WritesFilesWithMode0644(t *testing.T) {
	c, _ := newDownloadFixture(t, []dlFile{
		{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte(recipeBody))},
	}, nil)
	dest := t.TempDir()
	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dest, "recipes", "default.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %v, want 0644", got)
	}
}

// A manifest entry without a checksum cannot be verified or skipped; the file
// is still fetched and written.
func TestDownload_NoChecksumStillDownloads(t *testing.T) {
	c, fetches := newDownloadFixture(t, []dlFile{
		{path: "recipes/default.rb", body: recipeBody},
	}, nil)
	dest := t.TempDir()
	target := filepath.Join(dest, "recipes", "default.rb")
	writeTestFile(t, target, recipeBody)
	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n := atomic.LoadInt64(fetches["/files/recipes/default.rb"]); n != 1 {
		t.Errorf("fetches = %d, want 1", n)
	}
	assertFileContent(t, target, recipeBody)
}

// Filesystem failures at each step surface as errors and leave no temp file.
func TestDownload_FilesystemFailures(t *testing.T) {
	files := []dlFile{{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte(recipeBody))}}

	t.Run("parent path is a file", func(t *testing.T) {
		c, _ := newDownloadFixture(t, files, nil)
		dest := t.TempDir()
		writeTestFile(t, filepath.Join(dest, "recipes"), "not a dir")
		err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest)
		if err == nil || !strings.Contains(err.Error(), "create dirs") {
			t.Fatalf("err = %v, want a create-dirs failure", err)
		}
	})

	t.Run("destination is a directory", func(t *testing.T) {
		c, _ := newDownloadFixture(t, files, nil)
		dest := t.TempDir()
		// Non-empty, so the rename over it fails on every platform.
		writeTestFile(t, filepath.Join(dest, "recipes", "default.rb", "inner"), "x")
		err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest)
		if err == nil || !strings.Contains(err.Error(), "write file") {
			t.Fatalf("err = %v, want a write-file failure", err)
		}
		assertNoTempFiles(t, dest)
	})

	t.Run("directory not writable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		c, _ := newDownloadFixture(t, files, nil)
		dest := t.TempDir()
		dir := filepath.Join(dest, "recipes")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest)
		if err == nil || !strings.Contains(err.Error(), "create temp file") {
			t.Fatalf("err = %v, want a create-temp-file failure", err)
		}
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}
