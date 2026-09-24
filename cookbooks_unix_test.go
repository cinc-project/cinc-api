//go:build unix

package cinc

import (
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
