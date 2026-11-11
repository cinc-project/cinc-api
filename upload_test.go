package cinc

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func opener(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}

func TestClientUpload_PutsUnsignedBodyWithContentMD5(t *testing.T) {
	const content = "package 'nginx'\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("X-Ops-Authorization-1") != "" {
			t.Error("upload must not carry Chef signing headers")
		}
		sum := md5.Sum([]byte(content))
		if got := r.Header.Get("Content-MD5"); got != base64.StdEncoding.EncodeToString(sum[:]) {
			t.Errorf("Content-MD5 = %q", got)
		}
		if r.Header.Get("Content-Type") != "application/x-binary" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.ContentLength != int64(len(content)) || len(r.TransferEncoding) != 0 {
			t.Errorf("length %d, transfer-encoding %v; want an explicit length", r.ContentLength, r.TransferEncoding)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != content {
			t.Errorf("body = %q", b)
		}
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)

	if err := c.Upload(context.Background(), srv.URL+"/sandbox/x", md5Hex([]byte(content)), int64(len(content)), opener(content)); err != nil {
		t.Fatal(err)
	}
}

func TestClientUpload_RetriesWithFreshBody(t *testing.T) {
	const content = "data"
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)

	opens := 0
	open := func() (io.ReadCloser, error) { opens++; return io.NopCloser(strings.NewReader(content)), nil }
	if err := c.Upload(context.Background(), srv.URL+"/x", md5Hex([]byte(content)), int64(len(content)), open); err != nil {
		t.Fatal(err)
	}
	if opens != 2 || len(bodies) != 2 || bodies[1] != content {
		t.Fatalf("opens = %d, bodies = %q", opens, bodies)
	}
}

func TestClientUpload_EmptyBodyIsNotChunked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			t.Errorf("length %d, transfer-encoding %v; want Content-Length: 0", r.ContentLength, r.TransferEncoding)
		}
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)
	if err := c.Upload(context.Background(), srv.URL+"/x", md5Hex(nil), 0, opener("")); err != nil {
		t.Fatal(err)
	}
}

func TestClientUpload_RejectsBadChecksumAndOpenError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("request sent: %s", r.URL)
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)
	if err := c.Upload(context.Background(), srv.URL+"/x", "not-hex", 1, opener("x")); err == nil {
		t.Error("bad checksum accepted")
	}
	boom := func() (io.ReadCloser, error) { return nil, io.ErrUnexpectedEOF }
	if err := c.Upload(context.Background(), srv.URL+"/x", md5Hex([]byte("x")), 1, boom); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("open error = %v", err)
	}
}
