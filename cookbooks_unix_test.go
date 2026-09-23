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
