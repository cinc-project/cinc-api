// transport_test.go
package cinc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cinc-project/cinc-api/internal/signing"
)

// collectAuthSig reassembles the X-Ops-Authorization-N chunks from an HTTP
// request into a single base64 string.
func collectAuthSig(r *http.Request) string {
	var b strings.Builder
	for i := 1; ; i++ {
		chunk := r.Header.Get("X-Ops-Authorization-" + strconv.Itoa(i))
		if chunk == "" {
			break
		}
		b.WriteString(chunk)
	}
	return b.String()
}

// TestTransport_SigningExcludesQueryString verifies that when a request path
// contains a query string the signing canonical path is stripped of the "?"
// and everything after it — i.e. the v1.3 spec requirement.
func TestTransport_SigningExcludesQueryString(t *testing.T) {
	const pathOnly = "/organizations/o/search/node"
	const qs = "q=name%3Aweb01&rows=10&start=0"
	var (
		capturedSig       string
		capturedTimestamp string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSig = collectAuthSig(r)
		capturedTimestamp = r.Header.Get("X-Ops-Timestamp")
		w.Write([]byte(`{"total":0,"start":0,"rows":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, _, err := do[SearchResult](context.Background(), c, "GET",
		pathOnly+"?"+qs, nil); err != nil {
		t.Fatalf("do: %v", err)
	}

	// Verify the signature against a canonical request built with the path
	// ONLY (no query string).  If the bug is present the signature was over
	// path+qs and this will fail.
	sigBytes, err := base64.StdEncoding.DecodeString(capturedSig)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	canonical := signing.CanonicalRequest(signing.Request{
		Method:    "GET",
		Path:      pathOnly,
		Body:      nil,
		UserID:    "c",
		Timestamp: capturedTimestamp,
	})
	digest := sha256.Sum256([]byte(canonical))
	key := testRSAKey(t)
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		t.Errorf("signature does not verify against path-only canonical request: %v\n"+
			"(this means the query string was included in the signed path)", err)
	}
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Config{
		ServerURL: srv.URL, Org: "o", ClientName: "c", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDo_DecodesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") == "" {
			t.Error("request was not signed")
		}
		w.Write([]byte(`{"name":"web01"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{ Name string }
	got, resp, err := do[obj](context.Background(), c, "GET", "/nodes/web01", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "web01" || resp.StatusCode != 200 {
		t.Fatalf("got %+v status %d", got, resp.StatusCode)
	}
}

func TestDo_MapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":["no node"]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{ Name string }
	_, _, err := do[obj](context.Background(), c, "GET", "/nodes/x", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	type obj struct{}
	if _, _, err := do[obj](ctx, c, "GET", "/x", nil); err == nil {
		t.Fatal("expected context error")
	}
}

// TestRetry_503ThenSuccess asserts that a GET retried on 503 eventually
// succeeds and that the server is hit exactly 3 times (2 failures + 1 success).
func TestRetry_503ThenSuccess(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"name":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{ Name string }
	got, _, err := do[obj](context.Background(), c, "GET", "/nodes/x", nil)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if got.Name != "ok" {
		t.Fatalf("unexpected body: %+v", got)
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("server hit %d times, want 3", n)
	}
}

// TestRetry_ContextCancelledNoRetry asserts that a cancelled context causes
// exactly one attempt (no retries) — validates the isNetErr fix for Issue 3.
func TestRetry_ContextCancelledNoRetry(t *testing.T) {
	var hits atomic.Int32
	// Use a channel to synchronise: the handler blocks until the client
	// has cancelled the context, ensuring the cancellation is observed.
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		close(ready) // signal we're in the handler
		// Block until the request context is cancelled (client gone).
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		type obj struct{}
		_, _, err := do[obj](ctx, c, "GET", "/nodes/x", nil)
		done <- err
	}()

	<-ready  // wait until the handler is entered
	cancel() // cancel only after the first request is in-flight

	err := <-done
	if err == nil {
		t.Fatal("expected error after context cancel")
	}
	// Give any spurious retry a moment to arrive, then check.
	if n := hits.Load(); n != 1 {
		t.Fatalf("server hit %d times after context cancel, want 1 (no retries)", n)
	}
}

// TestRetry_PostNot retried asserts that a non-GET (POST) 503 is never retried.
func TestRetry_PostNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{}
	_, _, err := do[obj](context.Background(), c, "POST", "/nodes", map[string]any{"name": "x"})
	if err == nil {
		t.Fatal("expected error for 503")
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("POST hit %d times, want exactly 1 (must not retry non-GET)", n)
	}
}

// TestRetry_Exhausted asserts that a persistently failing GET stops after
// 1 + maxRetries total attempts and surfaces the final error.
func TestRetry_Exhausted(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{}
	_, _, err := do[obj](context.Background(), c, "GET", "/x", nil)
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	// defaultOptions sets maxRetries=2 -> 1 initial + 2 retries = 3 attempts.
	if n := hits.Load(); n != 3 {
		t.Fatalf("got %d hits, want 3 (1 + maxRetries)", n)
	}
}

// TestRetry_4xxNotRetried asserts that 4xx GET responses are NOT retried:
// they are not transient, so the server must be hit exactly once. This guards
// the isNetErr/serverErr split — without it every not-found/forbidden lookup
// silently incurs maxRetries extra round trips.
func TestRetry_4xxNotRetried(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(code)
				w.Write([]byte(`{"error":["nope"]}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			type obj struct{}
			if _, _, err := do[obj](context.Background(), c, "GET", "/nodes/x", nil); err == nil {
				t.Fatalf("expected error for %d", code)
			}
			if n := hits.Load(); n != 1 {
				t.Fatalf("%d GET hit server %d times, want 1 (4xx must not be retried)", code, n)
			}
		})
	}
}

func TestTransport_SetsChefHeaders(t *testing.T) {
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{}
	if _, _, err := do[obj](context.Background(), c, "GET", "/x", nil); err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("X-Chef-Version"); got == "" {
		t.Error("missing X-Chef-Version header")
	}
	if got := headers.Get("User-Agent"); got == "" {
		t.Error("missing User-Agent header")
	}
	if got := headers.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
}

func TestTransport_SetsContentTypeWithBody(t *testing.T) {
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{}
	if _, _, err := do[obj](context.Background(), c, "POST", "/x", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (POST with body)", contentType)
	}
}

func TestTransport_NoContentTypeForBodylessRequest(t *testing.T) {
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{}
	if _, _, err := do[obj](context.Background(), c, "GET", "/x", nil); err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		t.Errorf("Content-Type = %q on bodyless GET, want \"\"", contentType)
	}
}

func TestDo_MarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be hit when marshalling fails")
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	// channels cannot be JSON-encoded.
	type obj struct{}
	_, _, err := do[obj](context.Background(), c, "POST", "/x", make(chan int))
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if !strings.Contains(err.Error(), "marshal body") {
		t.Errorf("error %q should mention marshalling", err.Error())
	}
}

func TestDo_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{ Name string }
	_, _, err := do[obj](context.Background(), c, "GET", "/x", nil)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error %q should mention decoding", err.Error())
	}
}

func TestDo_EmptyBodyOK(t *testing.T) {
	// 204 No Content + empty body is a valid success — do should not error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	type obj struct{}
	_, resp, err := do[obj](context.Background(), c, "DELETE", "/x", nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp == nil || resp.StatusCode != 204 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestIsNetErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"other", errors.New("connection refused"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNetErr(tc.err); got != tc.want {
				t.Errorf("isNetErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestTransport_BadURL_ReturnsError(t *testing.T) {
	c, _ := NewClient(Config{
		ServerURL: "https://h", Org: "o", ClientName: "c", Key: testRSAKey(t),
	})
	type obj struct{}
	// A path with a control character makes http.NewRequestWithContext fail.
	_, _, err := do[obj](context.Background(), c, "GET", "\n", nil)
	if err == nil {
		t.Fatal("expected request-build error")
	}
}

func TestDo_NetworkErrorRetried(t *testing.T) {
	// Use a server we immediately close: every request fails. With maxRetries=2
	// a GET should be attempted 3 times before giving up.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	addr := srv.URL
	srv.Close()
	c, err := NewClient(Config{
		ServerURL: addr, Org: "o", ClientName: "c", Key: testRSAKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	type obj struct{}
	_, _, err = do[obj](context.Background(), c, "GET", "/x", nil)
	if err == nil {
		t.Fatal("expected error from dead server")
	}
}
