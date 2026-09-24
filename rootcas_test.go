package cinc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTLSStatusServer is an HTTPS server answering GET /_status, signed or not.
func newTLSStatusServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"pong"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func poolFor(srv *httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}

func newClientFor(t *testing.T, serverURL string, opts ...Option) *Client {
	t.Helper()
	c, err := NewClient(Config{ServerURL: serverURL, Org: "o", ClientName: "c", Key: testRSAKey(t)}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A server signed by a CA only the pool knows is trusted, and the pool
// replaces the system roots rather than adding to them.
func TestWithRootCAs_TrustsThePool(t *testing.T) {
	srv := newTLSStatusServer(t)

	c := newClientFor(t, srv.URL, WithRootCAs(poolFor(srv)), WithMaxRetries(0))
	if _, _, err := c.Status.Get(context.Background()); err != nil {
		t.Fatalf("Status.Get with the server's CA in the pool: %v", err)
	}

	// httptest servers all share one certificate, so an empty pool stands in
	// for a pool of some other CA.
	c = newClientFor(t, srv.URL, WithRootCAs(x509.NewCertPool()), WithMaxRetries(0))
	if _, _, err := c.Status.Get(context.Background()); err == nil {
		t.Fatal("Status.Get succeeded against a server the pool does not trust")
	}
}

// WithRootCAs keeps the library's default client, so its 30s timeout
// survives: that is the point of the option over WithHTTPClient.
func TestWithRootCAs_KeepsDefaultClient(t *testing.T) {
	pool := x509.NewCertPool()
	c := newClientFor(t, "https://h", WithRootCAs(pool))
	if c.httpClient.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want the default 30s", c.httpClient.Timeout)
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.httpClient.Transport)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs != pool {
		t.Fatal("RootCAs not set on the transport")
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("WithRootCAs turned off verification")
	}
	// http.DefaultTransport's tuning survives the clone.
	if !tr.ForceAttemptHTTP2 || tr.Proxy == nil {
		t.Error("default transport tuning dropped")
	}
	if def := http.DefaultTransport.(*http.Transport); def.TLSClientConfig != nil && def.TLSClientConfig.RootCAs == pool {
		t.Error("http.DefaultTransport was mutated")
	}
	if c.transferClient.Transport != c.httpClient.Transport {
		t.Error("bookshelf transfers do not trust the pool")
	}
}

// With WithHTTPClient the caller's client and transport are copied, not
// mutated, and everything else on them survives.
func TestWithRootCAs_WithHTTPClient(t *testing.T) {
	pool := x509.NewCertPool()
	custom := &http.Client{
		Timeout: 7 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 42,
			TLSClientConfig:     &tls.Config{ServerName: "internal.example"},
		},
	}
	c := newClientFor(t, "https://h", WithHTTPClient(custom), WithRootCAs(pool))
	if c.httpClient.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want the caller's 7s", c.httpClient.Timeout)
	}
	tr := c.httpClient.Transport.(*http.Transport)
	if tr.MaxIdleConnsPerHost != 42 || tr.TLSClientConfig.ServerName != "internal.example" {
		t.Errorf("caller's transport tuning dropped: %+v", tr)
	}
	if tr.TLSClientConfig.RootCAs != pool {
		t.Error("RootCAs not set")
	}
	orig := custom.Transport.(*http.Transport)
	if orig.TLSClientConfig.RootCAs != nil {
		t.Error("the caller's TLS config was mutated")
	}
}

// Both options can be given; skipping verification wins, since there is
// nothing left to verify against the pool.
func TestWithRootCAs_WithSkipTLSVerify(t *testing.T) {
	pool := x509.NewCertPool()
	c := newClientFor(t, "https://h", WithRootCAs(pool), WithSkipTLSVerify(true))
	cfg := c.httpClient.Transport.(*http.Transport).TLSClientConfig
	if !cfg.InsecureSkipVerify || cfg.RootCAs != pool {
		t.Errorf("TLS config = skip %v, roots %v; want both applied", cfg.InsecureSkipVerify, cfg.RootCAs)
	}

	// Against a server whose CA is nowhere, the request still goes through.
	srv := newTLSStatusServer(t)
	c = newClientFor(t, srv.URL, WithRootCAs(pool), WithSkipTLSVerify(true))
	if _, _, err := c.Status.Get(context.Background()); err != nil {
		t.Fatalf("Status.Get with verification skipped: %v", err)
	}
}

func TestWithRootCAs_NilIgnored(t *testing.T) {
	c := newClientFor(t, "https://h", WithRootCAs(nil))
	if c.httpClient.Transport != nil {
		t.Errorf("WithRootCAs(nil) replaced the transport with %T", c.httpClient.Transport)
	}
}

func TestClient_IdentityAccessors(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://chef.example.com/", Org: "acme", ClientName: "alice", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ServerURL(); got != "https://chef.example.com" {
		t.Errorf("ServerURL = %q, want the trailing slash trimmed", got)
	}
	if got := c.Org(); got != "acme" {
		t.Errorf("Org = %q", got)
	}
	if got := c.ClientName(); got != "alice" {
		t.Errorf("ClientName = %q", got)
	}
	if got := FormatServerURL(c.ServerURL(), c.Org()); got != "https://chef.example.com/organizations/acme" {
		t.Errorf("FormatServerURL(accessors) = %q", got)
	}
}

// A caller transport with no TLS config gets one holding just the pool. With
// HTTP/2 on, Transport.Clone gives the original a TLS config first, so the
// case needs HTTP/2 off (a non-nil TLSNextProto).
func TestWithRootCAs_TransportWithoutTLSConfig(t *testing.T) {
	pool := x509.NewCertPool()
	custom := &http.Client{Transport: &http.Transport{
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}}
	c := newClientFor(t, "https://h", WithHTTPClient(custom), WithRootCAs(pool))
	cfg := c.httpClient.Transport.(*http.Transport).TLSClientConfig
	if cfg == nil || cfg.RootCAs != pool || cfg.InsecureSkipVerify {
		t.Fatalf("TLS config = %+v, want only RootCAs set", cfg)
	}
	if custom.Transport.(*http.Transport).TLSClientConfig != nil {
		t.Error("the caller's transport was mutated")
	}
}
