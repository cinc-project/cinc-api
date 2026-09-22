package cinc

// MD5 is not a security control here: Chef's sandbox protocol identifies
// cookbook files by MD5 and the server compares against that, so the digest is
// dictated by the wire format rather than chosen.
import (
	"crypto/md5" //nolint:gosec // required by Chef's sandbox checksum protocol
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
)

// newMD5 returns a streaming MD5 hash, for digesting content too large to
// hold in memory at once.
func newMD5() hash.Hash {
	return md5.New() //nolint:gosec // see the note on the import
}

// fileMD5Hex returns the lower-case hex MD5 digest of the file at path.
func fileMD5Hex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to flush, nothing to lose
	h := newMD5()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// md5HexToBase64 re-encodes a hex MD5 digest as base64, the form Chef
// sandboxes expect in the Content-MD5 header. Both encode the same 16 bytes,
// so an upload derives the header from the checksum taken when the cookbook
// was walked instead of hashing the file again.
func md5HexToBase64(hexSum string) (string, error) {
	sum, err := hex.DecodeString(hexSum)
	if err != nil {
		return "", fmt.Errorf("checksum %q: %w", hexSum, err)
	}
	if len(sum) != md5.Size {
		return "", fmt.Errorf("checksum %q is not an MD5 digest", hexSum)
	}
	return base64.StdEncoding.EncodeToString(sum), nil
}
