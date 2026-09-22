// internal/signing/signer_test.go
package signing

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"strconv"
	"strings"
	"testing"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile("../../testdata/test_key.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSignHeaders_ContainsExpectedKeys(t *testing.T) {
	key := testKey(t)
	r := Request{Method: "GET", Path: "/nodes", UserID: "u",
		Timestamp: "2024-01-01T00:00:00Z"}
	h, err := SignHeaders(r, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"X-Ops-Sign", "X-Ops-Userid", "X-Ops-Timestamp",
		"X-Ops-Content-Hash", "X-Ops-Server-Api-Version", "X-Ops-Authorization-1"} {
		if h.Get(k) == "" {
			t.Errorf("missing header %s", k)
		}
	}
}

func TestSignHeaders_SignatureVerifies(t *testing.T) {
	key := testKey(t)
	r := Request{Method: "POST", Path: "/nodes", Body: []byte(`{"a":1}`),
		UserID: "u", Timestamp: "2024-01-01T00:00:00Z"}
	h, err := SignHeaders(r, key)
	if err != nil {
		t.Fatal(err)
	}
	var sig strings.Builder
	for i := 1; ; i++ {
		chunk := h.Get("X-Ops-Authorization-" + strconv.Itoa(i))
		if chunk == "" {
			break
		}
		sig.WriteString(chunk)
	}
	raw, err := base64.StdEncoding.DecodeString(sig.String())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(CanonicalRequest(r)))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], raw); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

func TestChunk60(t *testing.T) {
	got := chunk60(strings.Repeat("x", 130))
	if len(got) != 3 || len(got[0]) != 60 || len(got[2]) != 10 {
		t.Fatalf("chunk60 produced %d chunks: lens %d/%d/%d",
			len(got), len(got[0]), len(got[1]), len(got[2]))
	}
}

// A request may ask for a server API version other than the default. The
// version is part of the string-to-sign, so the header must carry the same
// value the signature covers.
func TestSignHeaders_APIVersion(t *testing.T) {
	key := testKey(t)
	for _, tc := range []struct{ in, want string }{{"", ServerAPIVersion}, {"2", "2"}} {
		r := Request{Method: "PUT", Path: "/p", UserID: "u", Timestamp: "t", APIVersion: tc.in}
		h, err := SignHeaders(r, key)
		if err != nil {
			t.Fatal(err)
		}
		if got := h.Get("X-Ops-Server-API-Version"); got != tc.want {
			t.Errorf("APIVersion %q: header = %q, want %q", tc.in, got, tc.want)
		}
		if got := CanonicalRequest(r); !strings.HasSuffix(got, "\nX-Ops-Server-API-Version:"+tc.want) {
			t.Errorf("APIVersion %q: CanonicalRequest = %q, want it to sign %s", tc.in, got, tc.want)
		}
	}
}
