package cinc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/signing"
)

// verifySignature re-verifies the v1.3 signature the way a real Chef Server
// does: over the path that actually arrived on the wire. It is the only way to
// catch a client that signs one path and sends another.
func verifySignature(t *testing.T, r *http.Request, key *rsa.PrivateKey) {
	t.Helper()
	var sig strings.Builder
	for i := 1; ; i++ {
		chunk := r.Header.Get("X-Ops-Authorization-" + strconv.Itoa(i))
		if chunk == "" {
			break
		}
		sig.WriteString(chunk)
	}
	raw, err := base64.StdEncoding.DecodeString(sig.String())
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	canonical := signing.CanonicalRequest(signing.Request{
		Method:    r.Method,
		Path:      r.URL.EscapedPath(),
		UserID:    r.Header.Get("X-Ops-Userid"),
		Timestamp: r.Header.Get("X-Ops-Timestamp"),
	})
	digest := sha256.Sum256([]byte(canonical))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], raw); err != nil {
		t.Errorf("signature does not cover the wire path %q: %v", r.URL.EscapedPath(), err)
	}
}

// A caller-supplied name is a single path segment: "/" and "." must not be
// able to walk out of the collection the service owns.
func TestRequestPath_NameCannotEscapeItsCollection(t *testing.T) {
	var wire string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire = r.URL.EscapedPath()
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	if _, err := c.Nodes.Delete(context.Background(), "../clients/validator"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	const want = "/organizations/o/nodes/..%2Fclients%2Fvalidator"
	if wire != want {
		t.Errorf("wire path = %q, want %q", wire, want)
	}
}

// Whatever reaches the server must be exactly what was signed, for every
// character a name might contain.
func TestRequestPath_SignatureCoversWirePath(t *testing.T) {
	key := testRSAKey(t)
	for _, name := range []string{"plain", "a b", "a#b", "a?b", "a/b", "a%b", "ünïcode"} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				verifySignature(t, r, key)
				w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			if _, _, err := c.Nodes.Get(context.Background(), name); err != nil {
				t.Fatalf("Get(%q): %v", name, err)
			}
		})
	}
}

// The org name comes from Config and lands in every org-scoped path.
func TestOrgPath_EscapesOrgName(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://h", Org: "a/b", ClientName: "c", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.orgPath("/nodes"), "/organizations/a%2Fb/nodes"; got != want {
		t.Errorf("orgPath = %q, want %q", got, want)
	}
}
