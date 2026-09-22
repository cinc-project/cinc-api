package cinc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// This file implements the Chef "encrypted data bag item" wire format so cinc
// can read and write secrets that are byte-for-byte compatible with knife and
// chef-client. The format wraps each value of a data bag item (every key
// except "id", which stays cleartext) in a small JSON object describing the
// cipher, IV, and ciphertext.
//
// Three wrapper versions exist in the wild:
//
//	v1 — AES-256-CBC, random 16-byte IV, PKCS#7 padding.
//	v2 — v1 plus an HMAC-SHA256 over the stored base64 ciphertext, keyed by
//	     the raw secret bytes, verified in constant time before decryption.
//	v3 — AES-256-GCM, random 12-byte nonce, 16-byte auth tag.
//
// For all versions the AES key is sha256(secret). Each plaintext value is
// JSON-encoded as {"json_wrapper": <value>} before encryption so that
// non-string values (numbers, bools, arrays, objects) round-trip cleanly.
//
// Decrypt reads v1/v2/v3; Encrypt writes v3 only (the modern, authenticated
// format that current Chef uses by default).

// Sentinel errors for encrypted data bag handling, for use with errors.Is.
var (
	// ErrNotEncrypted means a value (or item) is not a well-formed
	// encryption wrapper — typically the item simply isn't encrypted.
	ErrNotEncrypted = errors.New("cinc: data bag item is not encrypted")
	// ErrDataBagAuth means decryption failed authentication: a wrong
	// secret, a tampered ciphertext, or a bad v2 HMAC.
	ErrDataBagAuth = errors.New("cinc: data bag item failed authentication (wrong secret or tampered data)")
)

// jsonWrapperKey is the field Chef uses to box an arbitrary value so it can be
// JSON-serialized (and thus encrypted) regardless of its type.
const jsonWrapperKey = "json_wrapper"

// IsEncrypted reports whether every non-"id" top-level value is a well-formed
// encryption wrapper. An empty item, or one containing only "id", is not
// considered encrypted. Plaintext "chef_type" and "data_bag" values, which a
// Chef Server adds to the items it echoes back, are ignored, as Chef does.
func (i DataBagItem) IsEncrypted() bool {
	seen := false
	for k, v := range i {
		if k == "id" {
			continue
		}
		if _, err := parseWrapper(v); err != nil {
			if isServerItemKey(k) {
				continue
			}
			return false
		}
		seen = true
	}
	return seen
}

// Encrypt returns a new DataBagItem in which the "id" value is copied verbatim
// (in cleartext) and every other top-level value is replaced by a version-3
// (AES-256-GCM) encrypted wrapper. The receiver is not modified. It returns an
// error if the item has no non-empty string "id".
func (i DataBagItem) Encrypt(secret []byte) (DataBagItem, error) {
	id := i.ID()
	if id == "" {
		return nil, errors.New("cinc: data bag item requires a non-empty string \"id\" to encrypt")
	}
	key := deriveKey(secret)
	out := make(DataBagItem, len(i))
	for k, v := range i {
		if k == "id" {
			out[k] = v
			continue
		}
		w, err := encryptValueV3(key, v)
		if err != nil {
			return nil, fmt.Errorf("cinc: encrypting %q: %w", k, err)
		}
		out[k] = w
	}
	out["id"] = id
	return out, nil
}

// Decrypt returns a new DataBagItem in which "id" is copied verbatim and every
// other top-level value is treated as an encryption wrapper and decrypted. It
// reads wrapper versions 1, 2, and 3. The receiver is not modified.
//
// Plaintext "chef_type" and "data_bag" values (added by a Chef Server to the
// items it echoes back) are dropped rather than rejected, as Chef does; an
// encrypted value under either name is decrypted like any other.
//
// A value that is not a valid wrapper yields an ErrNotEncrypted error (so a
// caller can distinguish "this item isn't encrypted"); a failed authentication
// (wrong secret, tampering, bad HMAC) yields an ErrDataBagAuth error.
func (i DataBagItem) Decrypt(secret []byte) (DataBagItem, error) {
	out := make(DataBagItem, len(i))
	for k, v := range i {
		if k == "id" {
			out[k] = v
			continue
		}
		w, err := parseWrapper(v)
		if err != nil {
			if isServerItemKey(k) {
				continue
			}
			return nil, fmt.Errorf("cinc: decrypting %q: %w", k, err)
		}
		pt, err := w.decrypt(secret)
		if err != nil {
			return nil, fmt.Errorf("cinc: decrypting %q: %w", k, err)
		}
		out[k] = pt
	}
	return out, nil
}

// isServerItemKey reports whether k is one of the keys a Chef Server adds to
// a data bag item it echoes back (see serverItemKeys). Chef's
// DataBagItem.from_hash discards both before the encrypted-item reader runs.
func isServerItemKey(k string) bool {
	return k == serverItemKeys[0] || k == serverItemKeys[1]
}

// wrapper is a parsed encrypted-value envelope.
type wrapper struct {
	version       int
	cipher        string
	encryptedData string // base64 string exactly as stored (used un-stripped for v2 HMAC)
	iv            string
	authTag       string
	hmac          string
}

// parseWrapper validates that v is a well-formed encryption wrapper and
// extracts its fields. It returns an ErrNotEncrypted-wrapped error otherwise.
func parseWrapper(v any) (wrapper, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return wrapper{}, fmt.Errorf("%w: value is not an encryption wrapper", ErrNotEncrypted)
	}
	verF, ok := m["version"].(float64)
	if !ok {
		return wrapper{}, fmt.Errorf("%w: wrapper has no numeric \"version\"", ErrNotEncrypted)
	}
	ver := int(verF)
	if ver != 1 && ver != 2 && ver != 3 {
		return wrapper{}, fmt.Errorf("%w: unsupported wrapper version %d", ErrNotEncrypted, ver)
	}
	ed, ok := m["encrypted_data"].(string)
	if !ok {
		return wrapper{}, fmt.Errorf("%w: wrapper missing \"encrypted_data\"", ErrNotEncrypted)
	}
	iv, ok := m["iv"].(string)
	if !ok {
		return wrapper{}, fmt.Errorf("%w: wrapper missing \"iv\"", ErrNotEncrypted)
	}
	w := wrapper{version: ver, encryptedData: ed, iv: iv}
	if s, ok := m["cipher"].(string); ok {
		w.cipher = s
	}
	if ver == 2 {
		h, ok := m["hmac"].(string)
		if !ok {
			return wrapper{}, fmt.Errorf("%w: version 2 wrapper missing \"hmac\"", ErrNotEncrypted)
		}
		w.hmac = h
	}
	if ver == 3 {
		at, ok := m["auth_tag"].(string)
		if !ok {
			return wrapper{}, fmt.Errorf("%w: version 3 wrapper missing \"auth_tag\"", ErrNotEncrypted)
		}
		w.authTag = at
	}
	return w, nil
}

// decrypt returns the boxed plaintext value for the wrapper.
func (w wrapper) decrypt(secret []byte) (any, error) {
	key := deriveKey(secret)
	var plaintext []byte
	var err error
	switch w.version {
	case 1, 2:
		if w.version == 2 {
			if err := verifyHMAC(secret, w); err != nil {
				return nil, err
			}
		}
		plaintext, err = decryptCBC(key, w)
	case 3:
		plaintext, err = decryptGCM(key, w)
	default:
		return nil, fmt.Errorf("%w: unsupported wrapper version %d", ErrNotEncrypted, w.version)
	}
	if err != nil {
		return nil, err
	}
	return unboxValue(plaintext)
}

// deriveKey is the AES key for every version: sha256(secret).
func deriveKey(secret []byte) []byte {
	sum := sha256.Sum256(secret)
	return sum[:]
}

// boxValue serializes a value as {"json_wrapper": <value>} for encryption.
func boxValue(v any) ([]byte, error) {
	return json.Marshal(map[string]any{jsonWrapperKey: v})
}

// unboxValue parses encrypted-then-decrypted JSON and returns the boxed value.
func unboxValue(plaintext []byte) (any, error) {
	var box map[string]any
	if err := json.Unmarshal(plaintext, &box); err != nil {
		return nil, fmt.Errorf("%w: plaintext is not a json_wrapper object: %v", ErrDataBagAuth, err)
	}
	v, ok := box[jsonWrapperKey]
	if !ok {
		return nil, fmt.Errorf("%w: plaintext missing %q", ErrDataBagAuth, jsonWrapperKey)
	}
	return v, nil
}

// decodeB64 strips ALL whitespace (Chef/Ruby Base64.encode64 inserts newlines)
// before decoding standard base64.
func decodeB64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
}

// encodeB64 writes clean standard base64 with no line breaks.
func encodeB64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// encryptValueV3 boxes and AES-256-GCM encrypts a value into a v3 wrapper.
func encryptValueV3(key []byte, v any) (map[string]any, error) {
	plaintext, err := boxValue(v)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block) // 12-byte nonce, 16-byte tag
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)
	tagLen := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagLen]
	tag := sealed[len(sealed)-tagLen:]
	return map[string]any{
		"encrypted_data": encodeB64(ciphertext),
		"iv":             encodeB64(nonce),
		"auth_tag":       encodeB64(tag),
		"version":        float64(3),
		"cipher":         "aes-256-gcm",
	}, nil
}

// decryptGCM decrypts a version-3 (AES-256-GCM) wrapper.
func decryptGCM(key []byte, w wrapper) ([]byte, error) {
	ciphertext, err := decodeB64(w.encryptedData)
	if err != nil {
		return nil, fmt.Errorf("%w: bad encrypted_data base64: %v", ErrNotEncrypted, err)
	}
	nonce, err := decodeB64(w.iv)
	if err != nil {
		return nil, fmt.Errorf("%w: bad iv base64: %v", ErrNotEncrypted, err)
	}
	tag, err := decodeB64(w.authTag)
	if err != nil {
		return nil, fmt.Errorf("%w: bad auth_tag base64: %v", ErrNotEncrypted, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(nonce))
	if err != nil {
		return nil, fmt.Errorf("%w: bad nonce length %d: %v", ErrNotEncrypted, len(nonce), err)
	}
	plaintext, err := gcm.Open(nil, nonce, append(ciphertext, tag...), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDataBagAuth, err)
	}
	return plaintext, nil
}

// decryptCBC decrypts a version-1/2 (AES-256-CBC, PKCS#7) wrapper.
func decryptCBC(key []byte, w wrapper) ([]byte, error) {
	ciphertext, err := decodeB64(w.encryptedData)
	if err != nil {
		return nil, fmt.Errorf("%w: bad encrypted_data base64: %v", ErrNotEncrypted, err)
	}
	iv, err := decodeB64(w.iv)
	if err != nil {
		return nil, fmt.Errorf("%w: bad iv base64: %v", ErrNotEncrypted, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != block.BlockSize() {
		return nil, fmt.Errorf("%w: iv length %d, want %d", ErrNotEncrypted, len(iv), block.BlockSize())
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("%w: ciphertext length %d not a block multiple", ErrDataBagAuth, len(ciphertext))
	}
	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ciphertext)
	unpadded, err := pkcs7Unpad(out, block.BlockSize())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDataBagAuth, err)
	}
	return unpadded, nil
}

// verifyHMAC validates a version-2 wrapper's HMAC in constant time. The key is
// the RAW secret bytes (not the sha256 digest) and the message is the
// encrypted_data field exactly as stored (base64 string, whitespace and all).
func verifyHMAC(secret []byte, w wrapper) error {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(w.encryptedData))
	expected := mac.Sum(nil)
	got, err := decodeB64(w.hmac)
	if err != nil {
		return fmt.Errorf("%w: bad hmac base64: %v", ErrNotEncrypted, err)
	}
	if !hmac.Equal(expected, got) {
		return fmt.Errorf("%w: version 2 HMAC mismatch", ErrDataBagAuth)
	}
	return nil
}

// pkcs7Unpad removes PKCS#7 padding from a block-aligned plaintext.
func pkcs7Unpad(b []byte, blockSize int) ([]byte, error) {
	n := len(b)
	if n == 0 || n%blockSize != 0 {
		return nil, errors.New("invalid padded length")
	}
	pad := int(b[n-1])
	if pad == 0 || pad > blockSize {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, c := range b[n-pad:] {
		if int(c) != pad {
			return nil, errors.New("invalid PKCS#7 padding")
		}
	}
	return b[:n-pad], nil
}
