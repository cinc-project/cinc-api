package cinc

import (
	"crypto/md5" //nolint:gosec // Chef's sandbox protocol identifies files by MD5
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// md5Hex returns the lower-case hex MD5 digest of data, for building expected
// checksums in tests.
func md5Hex(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // see the note on the import
	return hex.EncodeToString(sum[:])
}

// md5HexToBase64Must is md5HexToBase64 for a digest known to be valid.
func md5HexToBase64Must(t *testing.T, hexSum string) string {
	t.Helper()
	b64, err := md5HexToBase64(hexSum)
	if err != nil {
		t.Fatal(err)
	}
	return b64
}

// tempCookbookFile writes content to a file and returns it as a walked
// cookbook file: its on-disk path and hex MD5, but not its content.
func tempCookbookFile(t *testing.T, content string) cookbookFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return cookbookFile{name: "f", path: path, checksum: md5Hex([]byte(content))}
}

func TestMD5Hex(t *testing.T) {
	// md5("") is a well-known constant.
	if got := md5Hex([]byte("")); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("md5Hex empty = %q", got)
	}
}

// The base64 Content-MD5 Chef sandboxes expect is the same 16-byte digest as
// the hex checksum, so it is derived from it rather than hashing the file a
// second time.
func TestMD5HexToBase64(t *testing.T) {
	if got := md5HexToBase64Must(t, "d41d8cd98f00b204e9800998ecf8427e"); got != "1B2M2Y8AsgTpgAmY7PhCfg==" {
		t.Fatalf("md5HexToBase64(md5 of empty) = %q", got)
	}
	// Upper-case hex (as a server might list it) decodes the same.
	if got := md5HexToBase64Must(t, "D41D8CD98F00B204E9800998ECF8427E"); got != "1B2M2Y8AsgTpgAmY7PhCfg==" {
		t.Fatalf("md5HexToBase64(upper) = %q", got)
	}
	for _, bad := range []string{"zz", "d41d8cd9", ""} {
		if _, err := md5HexToBase64(bad); err == nil {
			t.Errorf("md5HexToBase64(%q): want an error", bad)
		}
	}
}

func TestFileMD5Hex(t *testing.T) {
	f := tempCookbookFile(t, "hello")
	got, err := fileMD5Hex(f.path)
	if err != nil || got != md5Hex([]byte("hello")) {
		t.Fatalf("fileMD5Hex = %q, %v", got, err)
	}
	if _, err := fileMD5Hex(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("want an error for a missing file")
	}
	if _, err := fileMD5Hex(t.TempDir()); err == nil {
		t.Fatal("want an error reading a directory")
	}
}
