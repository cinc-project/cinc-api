package cinc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// A document whose bytes a decode/re-encode round trip would change: member
// order, a float with a zero fraction, and an integer above 2^53.
const exactDoc = `{"z":1,"a":1000.0,"big":9007199254740993}`

func TestClientDo_ReturnsResponseBytesUnchanged(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/acme/nodes/web01", cinctest.Route{Body: exactDoc})
	c := newTestClient(t, srv.Server)

	got, resp, err := c.Do(context.Background(), "GET", "/organizations/acme/nodes/web01", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != exactDoc {
		t.Fatalf("body = %s, want %s", got, exactDoc)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestClientDo_SendsBodyUnchangedToAbsolutePath(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /users/alice", cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, r *http.Request, body []byte) {
			if string(body) != exactDoc {
				t.Errorf("sent body = %s, want %s", body, exactDoc)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	if _, _, err := c.Do(context.Background(), "PUT", "/users/alice", []byte(exactDoc)); err != nil {
		t.Fatal(err)
	}
}

func TestClientDo_SignsWithRequestedServerAPIVersion(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users", cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, r *http.Request, _ []byte) {
			if v := r.Header.Get("X-Ops-Server-API-Version"); v != "2" {
				t.Errorf("X-Ops-Server-API-Version = %q, want 2", v)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	ctx := WithServerAPIVersion(context.Background(), "2")
	if _, _, err := c.Do(ctx, "GET", "/users", nil); err != nil {
		t.Fatal(err)
	}
}

func TestClientDo_Non2xxReturnsErrorResponseAndBody(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/ghost", cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)

	body, resp, err := c.Do(context.Background(), "GET", "/users/ghost", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	if resp == nil || resp.StatusCode != 404 || !strings.Contains(string(body), "not found") {
		t.Fatalf("resp = %+v body = %s", resp, body)
	}
}

func TestClientFetch_StreamsUnsignedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") != "" {
			t.Error("bookshelf fetch must not carry Chef signing headers")
		}
		_, _ = w.Write([]byte("file contents"))
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)

	var buf bytes.Buffer
	if err := c.Fetch(context.Background(), srv.URL+"/bookshelf/abc?sig=x", copyTo(&buf)); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "file contents" {
		t.Fatalf("fetched %q", buf.String())
	}
}

func TestClientFetch_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)

	var buf bytes.Buffer
	err := c.Fetch(context.Background(), srv.URL+"/x", copyTo(&buf))
	var er *ErrorResponse
	if !errors.As(err, &er) || er.StatusCode != http.StatusForbidden {
		t.Fatalf("err = %v, want 403 ErrorResponse", err)
	}
}

func TestClientFetch_HandlerErrorIsNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte("data"))
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)

	boom := errors.New("disk full")
	err := c.Fetch(context.Background(), srv.URL+"/x", copyTo(failingWriter{boom}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if calls != 1 {
		t.Fatalf("server called %d times, want 1", calls)
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

func TestClientFetch_MalformedURLIsError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)
	var buf bytes.Buffer
	if err := c.Fetch(context.Background(), "http://bad host/\x7f", copyTo(&buf)); err == nil || !strings.Contains(err.Error(), "build fetch request") {
		t.Fatalf("err = %v, want build fetch request error", err)
	}
}

func copyTo(w io.Writer) func(io.Reader) error {
	return func(r io.Reader) error {
		_, err := io.Copy(w, r)
		return err
	}
}

// A body cut off mid-stream is a wire failure: Fetch retries it, and the
// handler sees a fresh reader from the start of the body, never a
// continuation of the partial one.
func TestClientFetch_RetriesTruncatedBodyWithFreshReader(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("partial"))
			return // the server closes with 93 bytes owed
		}
		_, _ = w.Write([]byte("complete"))
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)

	var got []string
	err := c.Fetch(context.Background(), srv.URL+"/x", func(r io.Reader) error {
		b, err := io.ReadAll(r)
		got = append(got, string(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || got[len(got)-1] != "complete" {
		t.Fatalf("calls = %d, handler saw %q", calls, got)
	}
}
