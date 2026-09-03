package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestIntegration_CookbookUploadDownload drives the full three-step cookbook
// upload (sandbox -> file PUTs -> manifest PUT) and the download flow against a
// real cinc-zero server, then verifies every file round-trips byte-for-byte.
// This also exercises the parallel bookshelf upload/download path end to end.
func TestIntegration_CookbookUploadDownload(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	// Build a cookbook named "nginx" on disk (the name comes from the dir base).
	src := filepath.Join(t.TempDir(), "nginx")
	files := map[string]string{
		"metadata.rb":              "name 'nginx'\nversion '1.0.0'\n",
		"recipes/default.rb":       "package 'nginx'\n",
		"attributes/default.rb":    "default['nginx']['port'] = 80\n",
		"templates/nginx.conf.erb": "listen <%= node['nginx']['port'] %>;\n",
	}
	for rel, content := range files {
		writeFile(t, filepath.Join(src, filepath.FromSlash(rel)), content)
	}

	cb, err := cinc.LocalCookbookFromDir(src, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	if err := c.Cookbooks.Upload(ctx, cb); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// The uploaded cookbook should now be listed and gettable.
	list, _, err := c.Cookbooks.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list["nginx"]; !ok {
		t.Fatalf("nginx not in cookbook list: %v", list)
	}
	got, _, err := c.Cookbooks.Get(ctx, "nginx", "1.0.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CookbookName != "nginx" {
		t.Fatalf("cookbook_name = %q, want nginx", got.CookbookName)
	}
	if n := len(got.AllFiles()); n != len(files) {
		t.Fatalf("manifest lists %d files, want %d", n, len(files))
	}

	// Download into a fresh directory and verify every file round-trips.
	dest := t.TempDir()
	if err := c.Cookbooks.Download(ctx, "nginx", "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	for rel, want := range files {
		assertFile(t, filepath.Join(dest, filepath.FromSlash(rel)), want)
	}
}

// TestIntegration_CookbookArtifactUpload uploads a content-addressed cookbook
// artifact (Policyfile mode) and reads it back by identifier.
func TestIntegration_CookbookArtifactUpload(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	src := filepath.Join(t.TempDir(), "nginx")
	writeFile(t, filepath.Join(src, "metadata.rb"), "name 'nginx'\n")
	writeFile(t, filepath.Join(src, "recipes", "default.rb"), "package 'nginx'\n")

	cb, err := cinc.LocalCookbookFromDir(src, "0.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	const identifier = "0123456789abcdef0123456789abcdef01234567"
	if err := c.CookbookArtifacts.Upload(ctx, cb, identifier); err != nil {
		t.Fatalf("CookbookArtifacts.Upload: %v", err)
	}

	got, _, err := c.CookbookArtifacts.Get(ctx, "nginx", identifier)
	if err != nil {
		t.Fatalf("CookbookArtifacts.Get: %v", err)
	}
	if got.CookbookName != "nginx" {
		t.Fatalf("cookbook_name = %q, want nginx", got.CookbookName)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("file %s = %q, want %q", path, got, want)
	}
}
