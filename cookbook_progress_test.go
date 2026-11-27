package cinc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// progressRecorder collects the paths a progress callback reports. The
// callback may run on several goroutines at once.
type progressRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *progressRecorder) record(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
}

func (r *progressRecorder) sorted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Sorted(slices.Values(r.paths))
}

// WithUploadProgress reports each file the upload sends, by cookbook path:
// only the files the server lacked, since the others are never sent.
func TestUpload_WithUploadProgress(t *testing.T) {
	root := filepath.Join(t.TempDir(), "web")
	writeTree(t, root, map[string]string{
		"metadata.rb":        "name 'web'\nversion '1.0.0'\n",
		"recipes/default.rb": "package 'nginx'\n",
		"recipes/other.rb":   "package 'curl'\n",
	})
	cb, err := LocalCookbookFromDir(root, "")
	if err != nil {
		t.Fatal(err)
	}
	present := md5Hex([]byte("package 'curl'\n")) // the server already holds recipes/other.rb
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			var entries []string
			for _, f := range cb.files {
				needs := f.checksum != present
				entries = append(entries, `"`+f.checksum+`":{"needs_upload":`+map[bool]string{true: "true", false: "false"}[needs]+
					`,"url":"http://`+r.Host+`/shelf/`+f.checksum+`"}`)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"sandbox_id":"sb","checksums":{`+strings.Join(entries, ",")+`}}`)
		case strings.HasPrefix(r.URL.Path, "/shelf/"):
		case r.Method == "PUT":
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	var cookbook, artifact progressRecorder
	if err := c.Cookbooks.Upload(context.Background(), cb, WithUploadProgress(cookbook.record)); err != nil {
		t.Fatal(err)
	}
	if err := c.CookbookArtifacts.Upload(context.Background(), cb, "abc", WithUploadProgress(artifact.record)); err != nil {
		t.Fatal(err)
	}
	want := []string{"metadata.rb", "recipes/default.rb"}
	for name, got := range map[string][]string{"cookbook": cookbook.sorted(), "artifact": artifact.sorted()} {
		if !slices.Equal(got, want) {
			t.Errorf("%s progress = %v, want %v", name, got, want)
		}
	}
}

// A cookbook artifact cannot be frozen or forced: its identifier is a content
// hash and the server never replaces one. Those options are refused before
// anything is sent.
func TestCookbookArtifacts_Upload_RefusesFreezeAndForce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	cb := &LocalCookbook{Name: "web", Version: "1.0.0"}
	for _, opt := range []CookbookUploadOption{WithFreeze(), WithForce()} {
		err := c.CookbookArtifacts.Upload(context.Background(), cb, "abc", opt)
		if err == nil || !strings.Contains(err.Error(), "artifact") {
			t.Errorf("err = %v, want a refusal", err)
		}
	}
}

// WithDownloadProgress reports every file once it is in place, whether it was
// fetched or already on disk with the right checksum, so the reports add up to
// the manifest.
func TestDownloadFiles_WithDownloadProgress(t *testing.T) {
	content := map[string]string{"metadata.rb": "name 'web'\n", "recipes/default.rb": "package 'nginx'\n"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, content[strings.TrimPrefix(r.URL.Path, "/shelf/")])
	}))
	defer srv.Close()
	var refs []CookbookFileRef
	for p, body := range content {
		refs = append(refs, CookbookFileRef{Path: p, URL: srv.URL + "/shelf/" + p, Checksum: md5Hex([]byte(body))})
	}
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "metadata.rb"), []byte(content["metadata.rb"]), 0o644); err != nil {
		t.Fatal(err)
	}

	var progress progressRecorder
	c := newTestClient(t, srv)
	if err := c.Cookbooks.DownloadFiles(context.Background(), &Cookbook{AllFilesManifest: refs}, dest,
		WithDownloadProgress(progress.record)); err != nil {
		t.Fatal(err)
	}
	if got, want := progress.sorted(), []string{"metadata.rb", "recipes/default.rb"}; !slices.Equal(got, want) {
		t.Errorf("progress = %v, want %v", got, want)
	}
}
