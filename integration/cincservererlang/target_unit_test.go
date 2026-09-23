package cincservererlang

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTarget(t *testing.T, tf map[string]string) string {
	t.Helper()
	b, err := json.Marshal(tf)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func fullTarget() map[string]string {
	return map[string]string{
		"server_url": "https://203.0.113.10", "org": "o", "admin": "a",
		"key_path": "admin.pem", "ca_cert_path": "/abs/ca.pem",
		"stats_user": "statsuser", "stats_password": "pw",
	}
}

func TestLoadTarget(t *testing.T) {
	p := writeTarget(t, fullTarget())
	tf, err := loadTarget(p)
	if err != nil {
		t.Fatalf("loadTarget: %v", err)
	}
	if tf.KeyPath != filepath.Join(filepath.Dir(p), "admin.pem") || tf.CACertPath != "/abs/ca.pem" {
		t.Fatalf("paths = %q, %q; relative paths resolve against the file, absolute ones stay", tf.KeyPath, tf.CACertPath)
	}
}

func TestLoadTargetMissingFileMeansNoTarget(t *testing.T) {
	_, err := loadTarget(filepath.Join(t.TempDir(), "absent.json"))
	if !errors.Is(err, errNoTarget) {
		t.Fatalf("err = %v, want errNoTarget", err)
	}
}

func TestLoadTargetRejectsBadFiles(t *testing.T) {
	missingOrg := fullTarget()
	delete(missingOrg, "org")
	cases := map[string]string{
		"empty field": writeTarget(t, missingOrg),
		"not json":    filepath.Join(t.TempDir(), "bad.json"),
		"unreadable":  t.TempDir(), // a directory
	}
	if err := os.WriteFile(cases["not json"], []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, p := range cases {
		if _, err := loadTarget(p); err == nil || errors.Is(err, errNoTarget) {
			t.Errorf("%s: err = %v, want a real error", name, err)
		}
	}
}

func TestTargetPath(t *testing.T) {
	t.Setenv("CINC_SERVER_ERLANG_TARGET", "")
	if got := targetPath(); got != defaultTargetPath {
		t.Fatalf("targetPath() = %q, want the default", got)
	}
	t.Setenv("CINC_SERVER_ERLANG_TARGET", "/x/target.json")
	if got := targetPath(); got != "/x/target.json" {
		t.Fatalf("targetPath() = %q, want the environment's", got)
	}
}

func TestHTTPClientTrustingVerifiesAgainstTheCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	hc, err := httpClientTrusting(caPEM)
	if err != nil {
		t.Fatalf("httpClientTrusting: %v", err)
	}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET with the server's CA trusted: %v", err)
	}
	resp.Body.Close()

	// Every httptest TLS server shares one built-in certificate, so the
	// untrusted server needs a certificate of its own.
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	other.TLS = &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}}
	other.StartTLS()
	defer other.Close()
	if _, err := hc.Get(other.URL); err == nil {
		t.Fatal("GET to a server with a different CA succeeded; TLS is not being verified")
	}
	if _, err := httpClientTrusting([]byte("not a cert")); err == nil {
		t.Fatal("httpClientTrusting accepted a file with no certificate")
	}
}

func TestWaitReady(t *testing.T) {
	calls := 0
	err := waitReady(context.Background(), time.Second, time.Millisecond, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("waitReady = %v after %d calls, want nil after 3", err, calls)
	}

	err = waitReady(context.Background(), 20*time.Millisecond, time.Millisecond, func(context.Context) error {
		return errors.New("still booting")
	})
	if err == nil || !strings.Contains(err.Error(), "still booting") {
		t.Fatalf("waitReady = %v, want a timeout carrying the last error", err)
	}
}

// selfSignedCert returns a certificate for 127.0.0.1 signed by nobody the
// client trusts.
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "untrusted"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
