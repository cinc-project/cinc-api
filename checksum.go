package cinc

// MD5 is not a security control here: Chef's sandbox protocol identifies
// cookbook files by MD5 and the server compares against that, so the digest is
// dictated by the wire format rather than chosen.
import (
	"crypto/md5" //nolint:gosec // required by Chef's sandbox checksum protocol
	"encoding/base64"
	"encoding/hex"
)

// md5Hex returns the lower-case hex MD5 digest of data.
func md5Hex(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // see the note on the import
	return hex.EncodeToString(sum[:])
}

// md5Base64 returns the base64-encoded MD5 digest of data, as Chef sandboxes
// expect in the Content-MD5 header.
func md5Base64(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // see the note on the import
	return base64.StdEncoding.EncodeToString(sum[:])
}
