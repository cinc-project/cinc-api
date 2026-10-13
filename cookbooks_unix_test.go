//go:build unix

package cinc

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A special file (here a named pipe) is neither a regular file nor a link to
// one, so it is left out; reading it would block the walk.
func TestLocalCookbookFromDir_SkipsSpecialFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nginx")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "metadata.rb"), []byte("name 'nginx'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	cb, err := LocalCookbookFromDir(root, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	if len(cb.files) != 1 || cb.files[0].name != "metadata.rb" {
		t.Errorf("packed %+v, want just metadata.rb", cb.files)
	}
}

// With SkipChefignore the chefignore is never read, so one that cannot be
// read does not fail the load.
func TestLocalCookbookFromDir_SkipChefignoreDoesNotReadIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-0 file")
	}
	// The chefignore that applies sits in the parent directory (as in a
	// chef-repo), so it is not one of the cookbook's own files.
	repo := t.TempDir()
	root := filepath.Join(repo, "x")
	writeTree(t, repo, map[string]string{"x/metadata.rb": "name 'x'\n", "chefignore": "*.bak\n"})
	if err := os.Chmod(filepath.Join(repo, "chefignore"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalCookbookFromDir(root, ""); err == nil {
		t.Fatal("expected the default load to fail reading chefignore")
	}
	if _, err := LocalCookbookFromDir(root, "", SkipChefignore()); err != nil {
		t.Fatalf("SkipChefignore: %v", err)
	}
}

// A directory already in destDir that is a symlink out of it must not carry
// a download outside destDir, though the manifest path itself is clean.
func TestDownload_RefusesSymlinkedDirectoryOutOfDest(t *testing.T) {
	files := []dlFile{{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte(recipeBody))}}
	c, _ := newDownloadFixture(t, files, nil)
	dest, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dest, "recipes")); err != nil {
		t.Fatal(err)
	}

	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest); err == nil {
		t.Fatal("download through a symlink out of the destination succeeded")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("download wrote %d entries outside the destination: %v", len(entries), entries)
	}
}

// A file in destDir that is a symlink out of it is not trusted as an
// up-to-date copy, even when the file it points to has the right checksum:
// it is replaced by a real file, and the target is left alone.
func TestDownload_ReplacesSymlinkedFileOutOfDest(t *testing.T) {
	files := []dlFile{{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte(recipeBody))}}
	c, fetches := newDownloadFixture(t, files, nil)
	dest, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "default.rb")
	writeTestFile(t, target, recipeBody)
	link := filepath.Join(dest, "recipes", "default.rb")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n := *fetches["/files/recipes/default.rb"]; n != 1 {
		t.Errorf("fetched %d times, want 1: a link out of the destination is no cached copy", n)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("%s is %v, want a regular file", link, info.Mode())
	}
	assertFileContent(t, link, recipeBody)
	assertFileContent(t, target, recipeBody)
}

// A symlink that stays inside destDir is followed as before.
func TestDownload_FollowsSymlinkWithinDest(t *testing.T) {
	files := []dlFile{{path: "recipes/default.rb", body: recipeBody, checksum: md5Hex([]byte(recipeBody))}}
	c, _ := newDownloadFixture(t, files, nil)
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dest, "recipes")); err != nil {
		t.Fatal(err)
	}

	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "real", "default.rb"), recipeBody)
}
