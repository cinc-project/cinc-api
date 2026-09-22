// databag_crypto_test.go
package cinc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The decrypt-compatibility fixtures below were produced by the real Chef
// gem (chef/encrypted_data_bag_item) to prove cross-compatibility with the
// canonical Ruby implementation. They were generated with:
//
//	require "chef/encrypted_data_bag_item"
//	secret = "opensesame-super-secret-key"
//	Chef::EncryptedDataBagItem::Encryptor::Version1Encryptor.new(value, secret).for_encrypted_item
//	# (and Version2Encryptor / Version3Encryptor)
//
// The base64 fields carry Chef/Ruby's "\n"-every-60-chars-plus-trailing-"\n"
// wrapping verbatim, which exercises the whitespace-stripping read path.
const compatSecret = "opensesame-super-secret-key"

// Chef Version1Encryptor.new("hello world", secret).
const fixtureV1String = `{
  "encrypted_data": "MSa0fay80/gnrXL5WOHWRnI2/mFrc5VCp2VsCDJaMTk=\n",
  "iv": "s2ElSTGBePtOJxPuaOdazA==\n",
  "version": 1,
  "cipher": "aes-256-cbc"
}`

// Chef Version2Encryptor.new("hello world", secret).
const fixtureV2String = `{
  "encrypted_data": "YPayJpcFtqEphi40tnv8QWBwcvpakdxfctIeGOnakHE=\n",
  "hmac": "2SRvwbdKdRejpDkWZfpmrzsG09Cj3QwijFlWMIN3Glw=\n",
  "iv": "YMv0tjSdxQDOXjTHVFa/ew==\n",
  "version": 2,
  "cipher": "aes-256-cbc"
}`

// Chef Version3Encryptor.new("hello world", secret).
const fixtureV3String = `{
  "encrypted_data": "tmPS0vwip+VU5tXd23ekJIGw0CrikuoKeZGm1mqD\n",
  "iv": "ckznCKqh5nUXxHXB\n",
  "auth_tag": "M5LttZ2UqwNvEWLVRwbAeA==\n",
  "version": 3,
  "cipher": "aes-256-gcm"
}`

// Chef Version3Encryptor.new(42, secret) — proves non-string round-trip.
const fixtureV3Int = `{
  "encrypted_data": "RDTRDMZq/w2pFJMvRuuvNFCfaw==\n",
  "iv": "tdMv/vRnp4Xryjas\n",
  "auth_tag": "5z/jdDZ1Q0pw1XVACDpXeQ==\n",
  "version": 3,
  "cipher": "aes-256-gcm"
}`

// Chef Version3Encryptor.new({"a"=>1,"b"=>[true,false,"x"]}, secret).
const fixtureV3Obj = `{
  "encrypted_data": "eisqM7r0KvjJ8dp4hNnN8maGjkSJl4bpAp097ljk2vwY2LceD9RIYbY6l1rK\n",
  "iv": "4DBfruCypGNoY7Qq\n",
  "auth_tag": "ECi58KYM0A1UwCSFMD3Itw==\n",
  "version": 3,
  "cipher": "aes-256-gcm"
}`

func mustWrapper(t *testing.T, raw string) any {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatalf("decode wrapper: %v", err)
	}
	return w
}

// flipFirstB64Byte decodes a (possibly newline-wrapped) base64 string, flips
// a bit in its first byte, and re-encodes it as clean standard base64.
func flipFirstB64Byte(t *testing.T, s string) string {
	t.Helper()
	clean := strings.Join(strings.Fields(s), "")
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		t.Fatalf("decode b64: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty ciphertext")
	}
	raw[0] ^= 0x01
	return base64.StdEncoding.EncodeToString(raw)
}

func TestDataBagItem_EncryptDecrypt_RoundTrip(t *testing.T) {
	secret := []byte("a-much-longer-test-secret-value")
	orig := DataBagItem{
		"id":     "creds",
		"str":    "s3cret-password",
		"num":    float64(42),
		"flag":   true,
		"nested": map[string]any{"host": "db.local", "port": float64(5432)},
		"list":   []any{"a", float64(1), false},
	}

	enc, err := orig.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Receiver must not be mutated.
	if _, ok := orig["str"].(string); !ok || orig["str"] != "s3cret-password" {
		t.Fatalf("Encrypt mutated the receiver: %+v", orig)
	}
	// id stays cleartext.
	if enc["id"] != "creds" {
		t.Errorf("encrypted id = %v, want cleartext \"creds\"", enc["id"])
	}
	// Every non-id value must look encrypted, not equal to the plaintext.
	if enc["str"] == "s3cret-password" {
		t.Errorf("str was not encrypted")
	}
	if !enc.IsEncrypted() {
		t.Errorf("Encrypt output should report IsEncrypted() == true")
	}

	dec, err := enc.Decrypt(secret)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !reflect.DeepEqual(map[string]any(dec), map[string]any(orig)) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", dec, orig)
	}
}

func TestDataBagItem_Encrypt_RequiresID(t *testing.T) {
	secret := []byte("secret")
	for _, item := range []DataBagItem{
		{},
		{"k": "v"},
		{"id": ""},
		{"id": 42},
	} {
		if _, err := item.Encrypt(secret); err == nil {
			t.Errorf("Encrypt(%+v) should fail without a non-empty string id", item)
		}
	}
}

func TestDataBagItem_Encrypt_WritesVersion3(t *testing.T) {
	secret := []byte("secret")
	enc, err := DataBagItem{"id": "x", "v": "value"}.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	w, ok := enc["v"].(map[string]any)
	if !ok {
		t.Fatalf("wrapper not a JSON object: %T", enc["v"])
	}
	if v, _ := w["version"].(float64); v != 3 {
		t.Errorf("version = %v, want 3", w["version"])
	}
	if w["cipher"] != "aes-256-gcm" {
		t.Errorf("cipher = %v, want aes-256-gcm", w["cipher"])
	}
	if w["auth_tag"] == nil || w["iv"] == nil || w["encrypted_data"] == nil {
		t.Errorf("v3 wrapper missing fields: %+v", w)
	}
}

func TestDataBagItem_Decrypt_ChefCompat(t *testing.T) {
	secret := []byte(compatSecret)
	tests := []struct {
		name    string
		wrapper string
		want    any
	}{
		{"v1-string", fixtureV1String, "hello world"},
		{"v2-string", fixtureV2String, "hello world"},
		{"v3-string", fixtureV3String, "hello world"},
		{"v3-int", fixtureV3Int, float64(42)},
		{"v3-obj", fixtureV3Obj, map[string]any{"a": float64(1), "b": []any{true, false, "x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := DataBagItem{"id": "fix", "value": mustWrapper(t, tt.wrapper)}
			dec, err := item.Decrypt(secret)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !reflect.DeepEqual(dec["value"], tt.want) {
				t.Errorf("value = %#v, want %#v", dec["value"], tt.want)
			}
			if dec["id"] != "fix" {
				t.Errorf("id = %v, want cleartext", dec["id"])
			}
		})
	}
}

func TestDataBagItem_Decrypt_WrongSecret(t *testing.T) {
	// v3 fixture decrypted with the wrong secret must be an auth error.
	item := DataBagItem{"id": "fix", "value": mustWrapper(t, fixtureV3String)}
	_, err := item.Decrypt([]byte("totally-wrong-secret"))
	if err == nil {
		t.Fatal("expected an error decrypting with the wrong secret")
	}
	if !errors.Is(err, ErrDataBagAuth) {
		t.Errorf("err = %v, want ErrDataBagAuth chain", err)
	}
}

func TestDataBagItem_Decrypt_V2TamperedHMAC(t *testing.T) {
	w := mustWrapper(t, fixtureV2String).(map[string]any)
	w["hmac"] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	item := DataBagItem{"id": "fix", "value": w}
	_, err := item.Decrypt([]byte(compatSecret))
	if err == nil {
		t.Fatal("expected an error on tampered v2 hmac")
	}
	if !errors.Is(err, ErrDataBagAuth) {
		t.Errorf("err = %v, want ErrDataBagAuth chain", err)
	}
}

func TestDataBagItem_Decrypt_V3TamperedCiphertext(t *testing.T) {
	enc, err := DataBagItem{"id": "x", "v": "value"}.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	w := enc["v"].(map[string]any)
	// Flip a byte in the ciphertext by re-encoding tampered bytes.
	w["encrypted_data"] = flipFirstB64Byte(t, w["encrypted_data"].(string))
	if _, err := enc.Decrypt([]byte("secret")); !errors.Is(err, ErrDataBagAuth) {
		t.Errorf("tampered v3 ciphertext err = %v, want ErrDataBagAuth", err)
	}
}

func TestDataBagItem_Decrypt_NotAWrapper(t *testing.T) {
	// A plain (un-encrypted) item should give a clear, non-auth error.
	item := DataBagItem{"id": "x", "password": "plaintext"}
	_, err := item.Decrypt([]byte("secret"))
	if err == nil {
		t.Fatal("expected an error decrypting a non-encrypted item")
	}
	if errors.Is(err, ErrDataBagAuth) {
		t.Errorf("non-wrapper should not be an auth error: %v", err)
	}
	if !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("err = %v, want ErrNotEncrypted chain", err)
	}
}

func TestDataBagItem_IsEncrypted(t *testing.T) {
	v3 := mustWrapper(t, fixtureV3String)
	tests := []struct {
		name string
		item DataBagItem
		want bool
	}{
		{"empty", DataBagItem{}, false},
		{"only-id", DataBagItem{"id": "x"}, false},
		{"plaintext", DataBagItem{"id": "x", "p": "secret"}, false},
		{"one-wrapper", DataBagItem{"id": "x", "p": v3}, true},
		{"mixed", DataBagItem{"id": "x", "p": v3, "q": "plaintext"}, false},
		{"id-plus-wrapper", DataBagItem{"id": "x", "a": mustWrapper(t, fixtureV1String), "b": mustWrapper(t, fixtureV2String)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.item.IsEncrypted(); got != tt.want {
				t.Errorf("IsEncrypted() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Items that picked up erchef's "chef_type"/"data_bag" keys (from a POST/PUT
// response, or stored permanently because such an item was PUT back) must
// still be recognised and decrypted, as Chef's own reader does: Ruby's
// DataBagItem.from_hash drops both keys before EncryptedDataBagItem sees them.
func TestDecrypt_IgnoresServerAddedKeys(t *testing.T) {
	secret := []byte("s3cret")
	enc, err := DataBagItem{"id": "db", "password": "hunter2"}.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	enc["chef_type"] = "data_bag_item"
	enc["data_bag"] = "creds"

	if !enc.IsEncrypted() {
		t.Error("IsEncrypted = false for an encrypted item carrying server keys")
	}
	got, err := enc.Decrypt(secret)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	want := DataBagItem{"id": "db", "password": "hunter2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decrypt = %v, want %v", got, want)
	}

	// The server keys alone do not make an item encrypted.
	if (DataBagItem{"id": "db", "chef_type": "data_bag_item", "data_bag": "creds"}).IsEncrypted() {
		t.Error("IsEncrypted = true for an item with only id and server keys")
	}
}

// An item that legitimately has "data_bag"/"chef_type" keys is encrypted like
// any other, and Decrypt returns them: an encrypted value is real data.
func TestDecrypt_EncryptedServerKeyNamesRoundTrip(t *testing.T) {
	secret := []byte("s3cret")
	in := DataBagItem{"id": "db", "data_bag": "mine", "chef_type": "x"}
	enc, err := in.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !enc.IsEncrypted() {
		t.Error("IsEncrypted = false")
	}
	got, err := enc.Decrypt(secret)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("Decrypt = %v, want %v", got, in)
	}
}
