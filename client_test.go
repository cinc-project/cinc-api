// client_test.go
package cinc

import (
	"crypto/tls"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewClient_OK(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://chef.example.com/", Org: "myorg",
		ClientName: "node1", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.orgPath("/nodes"); got != "/organizations/myorg/nodes" {
		t.Fatalf("orgPath = %q", got)
	}
	if c.Nodes == nil {
		t.Fatal("Nodes service not wired")
	}
}

func TestNewClient_BadConfig(t *testing.T) {
	if _, err := NewClient(Config{Org: "o"}); err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestNewClient_BadURL(t *testing.T) {
	if _, err := NewClient(Config{
		ServerURL: "://bad", Org: "o", ClientName: "c", Key: testRSAKey(t),
	}); err == nil {
		t.Fatal("expected error for malformed URL")
	}
}

func TestClient_now(t *testing.T) {
	c, _ := NewClient(Config{
		ServerURL: "https://h", Org: "o", ClientName: "c", Key: testRSAKey(t),
	})
	c.clock = func() time.Time { return time.Unix(0, 0).UTC() }
	if got := strings.TrimSpace(c.timestamp()); got != "1970-01-01T00:00:00Z" {
		t.Fatalf("timestamp = %q", got)
	}
}

func TestNewClient_CachesBaseURLString(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://chef.example.com/", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The serialized base URL is cached once so the request hot path does not
	// re-serialize *url.URL on every call.
	if c.baseURLStr != c.baseURL.String() {
		t.Fatalf("baseURLStr = %q, want %q", c.baseURLStr, c.baseURL.String())
	}
	if c.baseURLStr != "https://chef.example.com" {
		t.Fatalf("baseURLStr = %q, want trailing slash trimmed", c.baseURLStr)
	}
}

func TestNewClient_TrimsTrailingSlash(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://chef.example.com/", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.baseURL.String(); got != "https://chef.example.com" {
		t.Fatalf("baseURL = %q, want trailing slash trimmed", got)
	}
}

func TestNewClient_OrgPathHandlesLeadingSlash(t *testing.T) {
	c, _ := NewClient(Config{
		ServerURL: "https://h", Org: "myorg",
		ClientName: "c", Key: testRSAKey(t),
	})
	// Either form should yield the same canonical path.
	if got := c.orgPath("nodes"); got != "/organizations/myorg/nodes" {
		t.Errorf("orgPath(no slash) = %q", got)
	}
	if got := c.orgPath("/nodes"); got != "/organizations/myorg/nodes" {
		t.Errorf("orgPath(slash) = %q", got)
	}
}

func TestNewClient_WiresAllServices(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://h", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Nodes == nil || c.Roles == nil || c.Environments == nil ||
		c.Clients == nil || c.DataBags == nil || c.Search == nil ||
		c.Cookbooks == nil || c.CookbookArtifacts == nil ||
		c.Keys == nil || c.Groups == nil ||
		c.Status == nil || c.License == nil ||
		c.Policies == nil || c.PolicyGroups == nil ||
		c.Orgs == nil || c.Users == nil ||
		c.Containers == nil || c.ACLs == nil ||
		c.RequiredRecipe == nil || c.Associations == nil ||
		c.Principals == nil || c.Universe == nil || c.Stats == nil ||
		c.PushJobs == nil {
		t.Fatal("at least one service is unwired")
	}
}

func TestNewClient_SkipTLSVerify(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://h", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	}, WithSkipTLSVerify(true))
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.httpClient.Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify not enabled by WithSkipTLSVerify(true)")
	}
}

func TestNewClient_SkipTLSVerify_PreservesDefaultTuning(t *testing.T) {
	c, err := NewClient(Config{
		ServerURL: "https://h", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	}, WithSkipTLSVerify(true))
	if err != nil {
		t.Fatal(err)
	}
	tr := c.httpClient.Transport.(*http.Transport)
	// Cloning http.DefaultTransport must preserve HTTP/2, proxy support, and
	// connection pooling rather than dropping them on the skipTLSVerify path.
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 dropped; HTTP/2 negotiation disabled")
	}
	if tr.Proxy == nil {
		t.Error("Proxy dropped; HTTPS_PROXY/NO_PROXY no longer honored")
	}
	if tr.DialContext == nil {
		t.Error("DialContext dropped; keepalive dialing lost")
	}
	def := http.DefaultTransport.(*http.Transport)
	if tr.IdleConnTimeout != def.IdleConnTimeout {
		t.Errorf("IdleConnTimeout = %v, want default %v", tr.IdleConnTimeout, def.IdleConnTimeout)
	}
}

func TestNewClient_SkipTLSVerify_PreservesCallerTransport(t *testing.T) {
	custom := &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: 42,
	}}
	c, err := NewClient(Config{
		ServerURL: "https://h", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	}, WithHTTPClient(custom), WithSkipTLSVerify(true))
	if err != nil {
		t.Fatal(err)
	}
	tr := c.httpClient.Transport.(*http.Transport)
	// A caller-supplied *http.Transport must have its tuning preserved, with
	// only InsecureSkipVerify flipped on.
	if tr.MaxIdleConnsPerHost != 42 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 42 (caller transport clobbered)", tr.MaxIdleConnsPerHost)
	}
	if cfg := tr.TLSClientConfig; cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify not enabled")
	}
}

func TestNewClient_SkipTLSVerify_PreservesExistingTLSConfig(t *testing.T) {
	custom := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{ServerName: "internal.example"},
	}}
	c, err := NewClient(Config{
		ServerURL: "https://h", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	}, WithHTTPClient(custom), WithSkipTLSVerify(true))
	if err != nil {
		t.Fatal(err)
	}
	cfg := c.httpClient.Transport.(*http.Transport).TLSClientConfig
	// Existing TLS settings are preserved; only InsecureSkipVerify is flipped.
	if cfg.ServerName != "internal.example" {
		t.Errorf("ServerName = %q, want preserved", cfg.ServerName)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify not enabled")
	}
	// The caller's TLS config must not be mutated in place.
	if custom.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Error("caller's TLSClientConfig mutated in place")
	}
}

func TestNewClient_SkipTLSVerify_PreservesTimeout(t *testing.T) {
	custom := &http.Client{Timeout: 99 * time.Second}
	c, err := NewClient(Config{
		ServerURL: "https://h", Org: "o",
		ClientName: "c", Key: testRSAKey(t),
	}, WithHTTPClient(custom), WithSkipTLSVerify(true))
	if err != nil {
		t.Fatal(err)
	}
	// The httpClient is replaced when skipTLSVerify is set; the timeout
	// must still match the original custom client's timeout.
	if c.httpClient.Timeout != 99*time.Second {
		t.Fatalf("timeout = %v, want 99s", c.httpClient.Timeout)
	}
	// Sanity check: the resulting Transport really does skip verification.
	if cfg := c.httpClient.Transport.(*http.Transport).TLSClientConfig; cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify not enabled")
	}
}
