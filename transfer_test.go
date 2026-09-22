package cinc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dropConnection kills the connection without writing a response, so the
// client sees a failure on the wire rather than an HTTP status.
func dropConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Fatalf("hijack: %v", err)
	}
	conn.Close()
}

// flakyShelf is a fake bookshelf that answers the first len(failures) requests
// with the given failure and every later one with ok. A failure is an HTTP
// status, or 0 to drop the connection. Every request is checked for Chef
// signing headers, and each request body is recorded.
type flakyShelf struct {
	*httptest.Server
	attempts atomic.Int32
	mu       sync.Mutex
	bodies   []string
	md5s     []string
}

func newFlakyShelf(t *testing.T, failures []int, ok http.HandlerFunc) *flakyShelf {
	t.Helper()
	s := &flakyShelf{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") != "" {
			t.Errorf("bookshelf %s %s carried Chef signing header", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(body))
		s.md5s = append(s.md5s, r.Header.Get("Content-MD5"))
		s.mu.Unlock()
		n := int(s.attempts.Add(1))
		if n <= len(failures) {
			if failures[n-1] == 0 {
				dropConnection(t, w)
				return
			}
			w.WriteHeader(failures[n-1])
			return
		}
		ok(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func respondOK(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func respondWith(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func TestUploadFile_RetriesTransientFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures []int
	}{
		{"server error", []int{http.StatusServiceUnavailable}},
		{"dropped connection", []int{0}},
		{"both, up to the retry limit", []int{http.StatusBadGateway, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shelf := newFlakyShelf(t, tc.failures, respondOK)
			c := newTestClient(t, shelf.Server)
			sleeps := recordSleeps(c)

			if err := c.uploadFile(context.Background(), shelf.URL+"/bookshelf/x", []byte("hello")); err != nil {
				t.Fatalf("uploadFile: %v", err)
			}
			if got, want := int(shelf.attempts.Load()), len(tc.failures)+1; got != want {
				t.Errorf("attempts = %d, want %d", got, want)
			}
			if len(*sleeps) != len(tc.failures) {
				t.Errorf("backoffs = %v, want %d", *sleeps, len(tc.failures))
			}
			// Each retry must send the whole body again, not a drained reader.
			for i, b := range shelf.bodies {
				if b != "hello" {
					t.Errorf("attempt %d body = %q, want %q", i+1, b, "hello")
				}
				if shelf.md5s[i] != md5Base64([]byte("hello")) {
					t.Errorf("attempt %d Content-MD5 = %q", i+1, shelf.md5s[i])
				}
			}
		})
	}
}

func TestUploadFile_GivesUpAfterMaxRetries(t *testing.T) {
	shelf := newFlakyShelf(t, []int{500, 500, 500, 500}, respondOK)
	c := newTestClient(t, shelf.Server)
	recordSleeps(c)

	err := c.uploadFile(context.Background(), shelf.URL+"/x", []byte("hello"))
	var er *ErrorResponse
	if !errors.As(err, &er) || er.StatusCode != 500 {
		t.Fatalf("err = %v, want the final 500", err)
	}
	if got := shelf.attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3 (1 + default maxRetries)", got)
	}
}

func TestUploadFile_DoesNotRetryClientErrors(t *testing.T) {
	shelf := newFlakyShelf(t, []int{http.StatusForbidden}, respondOK)
	c := newTestClient(t, shelf.Server)
	sleeps := recordSleeps(c)

	if err := c.uploadFile(context.Background(), shelf.URL+"/x", []byte("hello")); err == nil {
		t.Fatal("want the 403")
	}
	if got := shelf.attempts.Load(); got != 1 || len(*sleeps) != 0 {
		t.Errorf("attempts = %d, backoffs = %v; a 4xx must not be retried", got, *sleeps)
	}
}

func TestUploadFile_HonoursMaxRetriesZero(t *testing.T) {
	shelf := newFlakyShelf(t, []int{503}, respondOK)
	c := newTestClient(t, shelf.Server)
	c.opts.maxRetries = 0

	if err := c.uploadFile(context.Background(), shelf.URL+"/x", []byte("hello")); err == nil {
		t.Fatal("want the 503 with retries disabled")
	}
	if got := shelf.attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

func TestUploadFile_CancelledBackoffStopsRetrying(t *testing.T) {
	shelf := newFlakyShelf(t, []int{503, 503, 503}, respondOK)
	c := newTestClient(t, shelf.Server)
	c.sleep = func(context.Context, time.Duration) bool { return false }

	if err := c.uploadFile(context.Background(), shelf.URL+"/x", []byte("hello")); err == nil {
		t.Fatal("want the 503")
	}
	if got := shelf.attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

// downloadFrom fetches recipes/default.rb from shelf into dest, without
// waiting between retries.
func downloadFrom(t *testing.T, shelf *flakyShelf, checksum string, dest string) error {
	t.Helper()
	c := newTestClient(t, shelf.Server)
	recordSleeps(c)
	return c.downloadFile(context.Background(), shelf.URL+"/files/recipes/default.rb",
		filepath.Join(dest, "recipes", "default.rb"), checksum)
}

func TestDownloadFile_RetriesTransientFailures(t *testing.T) {
	sum := md5Hex([]byte(recipeBody))
	truncated := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, "package")
	}
	for _, tc := range []struct {
		name     string
		failures []int
		first    http.HandlerFunc // replaces the first attempt when set
	}{
		{"server error", []int{http.StatusInternalServerError}, nil},
		{"dropped connection", []int{0}, nil},
		{"truncated body", nil, truncated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			ok := func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 && tc.first != nil {
					tc.first(w, r)
					return
				}
				io.WriteString(w, recipeBody)
			}
			shelf := newFlakyShelf(t, tc.failures, ok)
			dest := t.TempDir()
			if err := downloadFrom(t, shelf, sum, dest); err != nil {
				t.Fatalf("downloadFile: %v", err)
			}
			if got := shelf.attempts.Load(); got != 2 {
				t.Errorf("attempts = %d, want 2", got)
			}
			assertFileContent(t, filepath.Join(dest, "recipes", "default.rb"), recipeBody)
			assertNoTempFiles(t, dest)
		})
	}
}

// Failures that would recur identically are not retried: a checksum mismatch
// (the server's content is wrong, not the transfer) and a local disk error.
func TestDownloadFile_DoesNotRetryDeterministicFailures(t *testing.T) {
	t.Run("checksum mismatch", func(t *testing.T) {
		shelf := newFlakyShelf(t, nil, respondWith(recipeBody))
		if err := downloadFrom(t, shelf, md5Hex([]byte("other")), t.TempDir()); err == nil {
			t.Fatal("want a checksum mismatch")
		}
		if got := shelf.attempts.Load(); got != 1 {
			t.Errorf("attempts = %d, want 1", got)
		}
	})
	t.Run("destination unwritable", func(t *testing.T) {
		shelf := newFlakyShelf(t, nil, respondWith(recipeBody))
		dest := t.TempDir()
		// A non-empty directory where the file should go: the rename fails.
		writeTestFile(t, filepath.Join(dest, "recipes", "default.rb", "inner"), "x")
		if err := downloadFrom(t, shelf, md5Hex([]byte(recipeBody)), dest); err == nil {
			t.Fatal("want a write failure")
		}
		if got := shelf.attempts.Load(); got != 1 {
			t.Errorf("attempts = %d, want 1", got)
		}
	})
}

// The API-call timeout must not cap a bookshelf transfer: a large file on a
// slow link legitimately takes longer than any sensible API round trip.
func TestTransfer_NotBoundByAPITimeout(t *testing.T) {
	shelf := newFlakyShelf(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		io.WriteString(w, recipeBody)
	})
	c, err := NewClient(Config{ServerURL: shelf.URL, Org: "o", ClientName: "c", Key: testRSAKey(t)},
		WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "f")
	if err := c.downloadFile(context.Background(), shelf.URL+"/f", dest, ""); err != nil {
		t.Fatalf("download bounded by the 50ms API timeout: %v", err)
	}
	if err := c.uploadFile(context.Background(), shelf.URL+"/f", []byte("x")); err != nil {
		t.Fatalf("upload bounded by the 50ms API timeout: %v", err)
	}
}

func TestTransfer_BoundByTransferTimeout(t *testing.T) {
	release := make(chan struct{})
	shelf := newFlakyShelf(t, nil, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	c, err := NewClient(Config{ServerURL: shelf.URL, Org: "o", ClientName: "c", Key: testRSAKey(t)},
		WithTransferTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	sleeps := recordSleeps(c)
	err = c.downloadFile(context.Background(), shelf.URL+"/f", filepath.Join(t.TempDir(), "f"), "")
	if err == nil || !strings.Contains(err.Error(), "Timeout") {
		t.Fatalf("err = %v, want a transfer timeout", err)
	}
	// Timeouts are deadline errors, which are not retried: a file too big
	// for the limit would only time out again.
	if len(*sleeps) != 0 {
		t.Errorf("backoffs = %v, want none", *sleeps)
	}
}

func TestTransfer_ClientSharesAPIClientTransport(t *testing.T) {
	tr := &http.Transport{}
	c, err := NewClient(Config{ServerURL: "https://h", Org: "o", ClientName: "c", Key: testRSAKey(t)},
		WithHTTPClient(&http.Client{Transport: tr, Timeout: time.Second}),
		WithTransferTimeout(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.transferClient.Transport != tr {
		t.Error("transfer client does not share the API client's transport")
	}
	if c.transferClient.Timeout != time.Minute {
		t.Errorf("transfer timeout = %v, want 1m", c.transferClient.Timeout)
	}
	if c.httpClient.Timeout != time.Second {
		t.Errorf("API timeout changed to %v", c.httpClient.Timeout)
	}
}

func TestTransfer_SkipTLSVerifyApplies(t *testing.T) {
	shelf := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") != "" {
			t.Errorf("bookshelf request carried Chef signing header")
		}
		io.WriteString(w, recipeBody)
	}))
	defer shelf.Close()
	c, err := NewClient(Config{ServerURL: shelf.URL, Org: "o", ClientName: "c", Key: testRSAKey(t)},
		WithSkipTLSVerify(true))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "f")
	if err := c.downloadFile(context.Background(), shelf.URL+"/f", dest, ""); err != nil {
		t.Fatalf("downloadFile over self-signed TLS: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Error(err)
	}
}
