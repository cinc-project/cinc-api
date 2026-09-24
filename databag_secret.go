package cinc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"
)

// ErrEmptyDataBagSecret means an encrypted data bag secret has nothing left
// once its surrounding whitespace is stripped. Chef refuses such a secret
// ("invalid zero length secret"), and so do LoadDataBagSecret and
// ParseDataBagSecret.
var ErrEmptyDataBagSecret = errors.New("cinc: data bag secret is empty")

// rubyStripCutset is the set of bytes Ruby's String#strip removes from both
// ends of a string: NUL and the ASCII whitespace characters. Unicode spaces
// such as U+00A0 are not in it, so bytes.TrimSpace would strip too much.
const rubyStripCutset = "\x00\t\n\v\f\r "

// LoadDataBagSecret reads an encrypted data bag secret file the way Chef's
// EncryptedDataBagItem.load_secret does, so a secret file behaves the same
// for this package, knife and chef-client. See ParseDataBagSecret for how
// the contents are interpreted. A read error is returned wrapped (so
// errors.Is(err, fs.ErrNotExist) works); an empty secret wraps
// ErrEmptyDataBagSecret and names the file.
//
// Chef's load_secret also accepts a URL ("https://host/secret") and fetches
// the secret from it. LoadDataBagSecret does not: path is always a local
// file. Fetch a remote secret yourself and pass it to ParseDataBagSecret.
func LoadDataBagSecret(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cinc: reading data bag secret: %w", err)
	}
	secret, err := ParseDataBagSecret(data)
	if err != nil {
		return nil, fmt.Errorf("%w (in %s)", err, path)
	}
	return secret, nil
}

// ParseDataBagSecret interprets the contents of an encrypted data bag secret
// file as Chef does (IO.read(path).strip):
//
//   - Leading and trailing NUL bytes and ASCII whitespace (space, \t, \n,
//     \v, \f, \r) are removed, as Ruby's String#strip removes them. Inner
//     whitespace, and Unicode whitespace at either end, is kept.
//   - The contents must be valid UTF-8. Chef reads the file as UTF-8 text
//     and String#strip raises on an invalid byte sequence, so knife and
//     chef-client cannot use such a file.
//   - A secret that is empty once stripped is refused with
//     ErrEmptyDataBagSecret.
//
// The returned bytes are the secret itself (the AES key is derived from them
// by Encrypt and Decrypt) and never alias data. A secret given literally on a
// command line, as knife's --secret, is used exactly as given and needs no
// parsing.
func ParseDataBagSecret(data []byte) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("cinc: data bag secret is not valid UTF-8, which Chef cannot read")
	}
	secret := bytes.Trim(data, rubyStripCutset)
	if len(secret) == 0 {
		return nil, ErrEmptyDataBagSecret
	}
	return bytes.Clone(secret), nil
}
