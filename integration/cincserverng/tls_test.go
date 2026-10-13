package cincserverng

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// testCA is a throwaway CA and a server certificate it issued for 127.0.0.1,
// the shape of a private-CA deployment: the client trusts the CA, never the
// leaf directly.
type testCA struct {
	pool              *x509.CertPool
	certFile, keyFile string
}

func newTestCA(dir string) (*testCA, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cinc-api integration CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}

	ca := &testCA{
		pool:     x509.NewCertPool(),
		certFile: filepath.Join(dir, "server.crt"),
		keyFile:  filepath.Join(dir, "server.key"),
	}
	ca.pool.AddCert(caCert)
	// The full chain, as an ACME client writes it.
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	if err := os.WriteFile(ca.certFile, chain, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(ca.keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	return ca, nil
}

// A client built the way an application builds one, trusting the private CA
// through WithRootCAs alone, must verify the server, sign requests the server
// accepts, and keep HTTP/2: replacing the TLS config of a transport is where
// a Go client most often loses it.
func TestTLSWithRootCAs(t *testing.T) {
	key := tlsTarget.Key
	cfg := cinc.Config{ServerURL: tlsTarget.ServerURL, Org: org, ClientName: tlsTarget.Admin, Key: key}

	// Baseline: without the CA the server's certificate is refused.
	untrusting, err := cinc.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := untrusting.Status.Get(t.Context()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("Status.Get without the CA = %v, want a certificate error", err)
	}

	c, err := cinc.NewClient(cfg, cinc.WithRootCAs(tlsPool))
	if err != nil {
		t.Fatal(err)
	}
	var proto string
	ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
		TLSHandshakeDone: func(cs tls.ConnectionState, _ error) { proto = cs.NegotiatedProtocol },
	})
	if _, _, err := c.Status.Get(ctx); err != nil {
		t.Fatalf("Status.Get: %v", err)
	}
	if proto != "h2" {
		t.Fatalf("negotiated protocol = %q, want h2", proto)
	}
	// A signed request over the same connection.
	if _, _, err := c.Nodes.List(context.WithoutCancel(ctx)); err != nil {
		t.Fatalf("signed Nodes.List over HTTP/2: %v", err)
	}
}
