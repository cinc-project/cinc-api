package cinc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Do takes any path, so the signature must cover what net/http actually puts
// on the wire (erchef verifies against the raw request path), not the string
// the caller passed in. A method is signed upper-case, as erchef canonicalizes
// it before verifying.
func TestDo_SignsTheWirePath(t *testing.T) {
	key := testRSAKey(t)
	for _, tc := range []struct {
		name, method, path, wantMethod string
	}{
		{"space", "GET", "/organizations/o/nodes/a b", "GET"},
		{"non-ASCII", "GET", "/organizations/o/nodes/é", "GET"},
		{"fragment", "GET", "/organizations/o/nodes/x#y", "GET"},
		{"query", "GET", "/organizations/o/search/node?q=a:b", "GET"},
		{"lower-case method", "get", "/organizations/o/nodes/x", "GET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.wantMethod {
					t.Errorf("wire method = %q, want %q", r.Method, tc.wantMethod)
				}
				verifySignature(t, r, key)
				_, _ = io.WriteString(w, `{}`)
			}))
			defer srv.Close()
			if _, _, err := newTestClient(t, srv).Do(context.Background(), tc.method, tc.path, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// erchef parses X-Ops-Server-API-Version as an integer and re-formats it to
// rebuild the string it verifies, so "01" must be signed (and sent) as "1",
// and a non-integer is refused before it can come back as a baffling 401.
func TestServerAPIVersion_IsCanonicalized(t *testing.T) {
	key := testRSAKey(t)
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Ops-Server-API-Version")
		verifySignature(t, r, key)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, _, err := c.Do(WithServerAPIVersion(context.Background(), "02"), "GET", "/x", nil); err != nil {
		t.Fatal(err)
	}
	if got != "2" {
		t.Errorf("API version header = %q, want %q", got, "2")
	}
	for _, bad := range []string{"abc", "-1", "1.5"} {
		got = ""
		_, _, err := c.Do(WithServerAPIVersion(context.Background(), bad), "GET", "/x", nil)
		if err == nil || !strings.Contains(err.Error(), "server API version") {
			t.Errorf("version %q: err = %v, want an invalid server API version error", bad, err)
		}
		if got != "" {
			t.Errorf("version %q reached the server", bad)
		}
	}
}

// The base URL is scheme://host[:port]; the organization comes from Config.Org.
// A pasted knife chef_server_url (…/organizations/acme) would otherwise send
// every request to /organizations/acme/organizations/acme/….
func TestNewClient_RejectsServerURLWithPath(t *testing.T) {
	for _, u := range []string{
		"https://chef.example.com/organizations/acme",
		"https://chef.example.com/chef",
		"https://chef.example.com?x=1",
		"https://chef.example.com#frag",
	} {
		_, err := NewClient(Config{ServerURL: u, Org: "o", ClientName: "c", Key: testRSAKey(t)})
		if err == nil || !strings.Contains(err.Error(), "ParseServerURL") {
			t.Errorf("ServerURL %q: err = %v, want a rejection pointing at ParseServerURL", u, err)
		}
	}
	for _, u := range []string{"https://chef.example.com", "https://chef.example.com/", "http://127.0.0.1:8889"} {
		if _, err := NewClient(Config{ServerURL: u, Org: "o", ClientName: "c", Key: testRSAKey(t)}); err != nil {
			t.Errorf("ServerURL %q: %v", u, err)
		}
	}
}

// An http.Client timeout is a transient wire failure like any other: chef-client
// retries Timeout::Error. It must not be mistaken for the caller's own context
// deadline, which does end the retries.
func TestRetry_HTTPClientTimeoutOnGETIsRetried(t *testing.T) {
	var attempts atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	defer close(release)
	c, err := NewClient(Config{ServerURL: srv.URL, Org: "o", ClientName: "c", Key: testRSAKey(t)},
		WithHTTPClient(&http.Client{Timeout: 100 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	recordSleeps(c)
	if _, _, err := do[map[string]any](context.Background(), c, "GET", "/x", nil); err != nil {
		t.Fatalf("want the retry to succeed, got %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// When the caller's context ends while the client waits to retry, the error
// says so (errors.Is(err, context.Canceled)) and still carries the failure
// that prompted the retry.
func TestRetry_CancelDuringBackoffReportsContextError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(context.Context, time.Duration) bool { cancel(); return false }

	_, _, err := do[map[string]any](ctx, c, "POST", "/x", map[string]any{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to match context.Canceled", err)
	}
	if _, ok := errors.AsType[*ErrorResponse](err); !ok {
		t.Errorf("err = %v, want it to still carry the 503 *ErrorResponse", err)
	}
}

// Retries back off exponentially but never wait unboundedly, even with a large
// WithMaxRetries, and the shift cannot overflow into a negative (zero) wait.
// The wait is jittered so parallel clients do not retry in lockstep.
func TestBackoff_IsCappedAndJittered(t *testing.T) {
	c := newTestClient(t, httptest.NewServer(http.NotFoundHandler()))
	c.opts.maxRetries = 100
	sleeps := recordSleeps(c)
	for attempt := range 100 {
		c.backoff(context.Background(), attempt, 0)
	}
	distinct := map[time.Duration]bool{}
	for i, d := range *sleeps {
		if d <= 0 || d > maxBackoff {
			t.Fatalf("attempt %d waits %v, want within (0, %v]", i, d, maxBackoff)
		}
		if i >= 20 {
			distinct[d] = true
		}
	}
	if len(distinct) < 2 {
		t.Errorf("capped waits are all identical (%v): want jitter", distinct)
	}
	// Retry-After still sets a floor.
	*sleeps = nil
	c.backoff(context.Background(), 0, 7*time.Second)
	if (*sleeps)[0] != 7*time.Second {
		t.Errorf("Retry-After floor: waited %v, want 7s", (*sleeps)[0])
	}
}

// 501 Not Implemented and 505 HTTP Version Not Supported are 5xx answers that
// no number of repeats will change.
func TestRetry_PermanentServerErrorsAreNotRetried(t *testing.T) {
	for _, code := range []int{http.StatusNotImplemented, http.StatusHTTPVersionNotSupported} {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(code)
		}))
		c := newTestClient(t, srv)
		recordSleeps(c)
		_, _, _ = do[map[string]any](context.Background(), c, "GET", "/x", nil)
		if got := attempts.Load(); got != 1 {
			t.Errorf("status %d: attempts = %d, want 1", code, got)
		}
		srv.Close()
	}
}

// erchef's error bodies come in more shapes than {"error": "..."} and
// {"error": ["..."]}; the human-readable text must survive each of them.
func TestErrorResponse_ErchefBodyShapes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		// oc_chef_wm_base:invalid_server_api_version_response
		{"slug plus message", `{"error":"invalid-x-ops-server-api-version","message":"Specified version 9 not supported","min_version":0,"max_version":2}`,
			[]string{"invalid-x-ops-server-api-version", "Specified version 9 not supported"}},
		// chef_wm_depsolver via chef_wm_util:error_message_envelope
		{"object elements", `{"error":[{"message":"Run list contains invalid items: no such cookbook nope.","non_existent_cookbooks":["nope"],"cookbooks_with_no_versions":[]}]}`,
			[]string{"Run list contains invalid items: no such cookbook nope."}},
		{"strings", `{"error":["a","b"]}`, []string{"a", "b"}},
		{"string", `{"error":"a"}`, []string{"a"}},
		{"not JSON", `<html>bad gateway</html>`, []string{"<html>bad gateway</html>"}},
		// Nothing readable in "error": fall back to the raw body.
		{"unreadable elements", `{"error":[1,{"code":2}]}`, []string{`{"error":[1,{"code":2}]}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newErrorResponse("GET", "/x", 400, []byte(tc.body)).Messages
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("Messages = %q, want %q", got, tc.want)
			}
		})
	}
}

// A pre-signed bookshelf/S3 URL carries its credentials in the query string;
// they must not end up in an error message (and so in logs).
func TestTransferErrors_OmitPresignedQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	recordSleeps(c)
	secret := "X-Amz-Signature=deadbeef"
	err := c.Fetch(context.Background(), srv.URL+"/bookshelf/x?"+secret, func(io.Reader) error { return nil })
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("HTTP error = %v, want one without the query string", err)
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	err = c.Fetch(context.Background(), dead.URL+"/bookshelf/x?"+secret, func(io.Reader) error { return nil })
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("wire error = %v, want one without the query string", err)
	}
}
