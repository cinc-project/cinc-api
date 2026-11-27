package cinc

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// A request path with an empty, "." or ".." segment never reaches the server.
// The server's router drops an empty trailing segment, so an empty item id
// would turn DELETE /data/<bag>/<id> into DELETE /data/<bag>: the whole bag.
func TestRequestPath_RefusesEmptyAndDotSegments(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"empty item id":   func() error { _, err := c.DataBags.Items("b").Delete(ctx, ""); return err },
		"dot item id":     func() error { _, err := c.DataBags.Items("b").Delete(ctx, "."); return err },
		"dot-dot node":    func() error { _, _, err := c.Nodes.Get(ctx, ".."); return err },
		"empty bag":       func() error { _, err := c.DataBags.Items("").Delete(ctx, "x"); return err },
		"empty node":      func() error { _, err := c.Nodes.Delete(ctx, ""); return err },
		"Do double slash": func() error { _, _, err := c.Do(ctx, "GET", "/organizations/o//nodes", nil); return err },
		"Do dot segment":  func() error { _, _, err := c.Do(ctx, "GET", "/organizations/o/nodes/../x", nil); return err },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "path segment") {
			t.Errorf("%s: err = %v, want a path segment error", name, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached the server", n)
	}

	// A query string may hold anything, and a name that merely contains dots
	// is a name.
	if _, _, err := c.Do(ctx, "GET", "/organizations/o/search/node?q=a//b/..", nil); err != nil {
		t.Errorf("query with slashes and dots: %v", err)
	}
	if _, _, err := c.Nodes.Get(ctx, "web..01"); err != nil {
		t.Errorf("dotted name: %v", err)
	}
}

// Every Chef GET answers with a JSON document. A 2xx with an empty or null
// body (a misbehaving proxy or cache) must not decode as an empty object:
// Groups.AddMembers and ACLs.Grant would write that back, dropping every
// existing member, and Users.SetPassword would panic on the nil map.
func TestGET_EmptyBodyIsAnError(t *testing.T) {
	for _, body := range []string{"", "null", " null\n"} {
		var puts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				puts.Add(1)
			}
			w.Write([]byte(body))
		}))
		c := newTestClient(t, srv)
		ctx := context.Background()

		if _, err := c.Groups.AddMembers(ctx, "admins", MemberUser, "bob"); err == nil {
			t.Errorf("body %q: Groups.AddMembers succeeded", body)
		}
		if _, err := c.ACLs.Grant(ctx, ObjectACL(ACLNodes, "web"), "read", ACLMembers{Users: []string{"bob"}}); err == nil {
			t.Errorf("body %q: ACLs.Grant succeeded", body)
		}
		if _, err := c.Users.SetPassword(ctx, "bob", "pw"); err == nil {
			t.Errorf("body %q: Users.SetPassword succeeded", body)
		}
		if _, _, err := c.Nodes.Get(ctx, "web"); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Errorf("body %q: Nodes.Get err = %v, want an empty-body error", body, err)
		}
		if n := puts.Load(); n != 0 {
			t.Errorf("body %q: %d writes reached the server", body, n)
		}
		srv.Close()
	}

	// A write may answer with nothing: that is not an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	if _, err := newTestClient(t, srv).Nodes.Delete(context.Background(), "web"); err != nil {
		t.Errorf("DELETE with an empty body: %v", err)
	}
}

// A server whose total promises rows it never returns must not keep the
// iterator paging for ever: a run of empty pages is an error, not silence,
// since stopping quietly would truncate the results.
func TestSearch_EmptyPagesDoNotRunForever(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"total":1000000000000,"start":0,"rows":[]}`))
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv).Search.Collect(context.Background(), "node", "*:*", WithRows(10))
	if err == nil || !strings.Contains(err.Error(), "empty pages") {
		t.Errorf("err = %v, want an empty-pages error", err)
	}
	if n := hits.Load(); n != maxEmptySearchPages {
		t.Errorf("requests = %d, want %d", n, maxEmptySearchPages)
	}
}

// The next offset cannot overflow into a negative start.
func TestSearch_HugeRowsDoNotOverflow(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"total":1000000000000,"start":1,"rows":[{"name":"a"}]}`))
	}))
	defer srv.Close()
	rows, err := newTestClient(t, srv).Search.Collect(context.Background(), "node", "*:*", WithStart(1), WithRows(math.MaxInt))
	if err != nil || len(rows) != 1 || hits.Load() != 1 {
		t.Errorf("Collect = %d rows, %v after %d requests; want 1 row from 1 request", len(rows), err, hits.Load())
	}
}

// Two manifest entries that land on one file would race two writers onto it,
// one silently winning. Paths equal once cleaned always collide; paths equal
// but for case collide on the case-insensitive filesystems macOS and Windows
// use by default.
func TestDownloadFiles_RefusesCollidingPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("nothing should be fetched")
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ref := func(p string) CookbookFileRef { return CookbookFileRef{Path: p, URL: srv.URL + "/f"} }

	err := c.Cookbooks.DownloadFiles(context.Background(),
		&Cookbook{AllFilesManifest: []CookbookFileRef{ref("recipes/x.rb"), ref("recipes/./x.rb")}}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Errorf("cleaned duplicate: err = %v, want a same-file error", err)
	}

	err = c.Cookbooks.DownloadFiles(context.Background(),
		&Cookbook{AllFilesManifest: []CookbookFileRef{ref("recipes/default.rb"), ref("recipes/DEFAULT.rb")}}, t.TempDir())
	if caseInsensitiveFS() {
		if err == nil || !strings.Contains(err.Error(), "same file") {
			t.Errorf("case-fold duplicate on %s: err = %v, want a same-file error", runtime.GOOS, err)
		}
	}
}

// A decrypted value is one JSON document; trailing bytes mean it is corrupt.
func TestDecrypt_RejectsTrailingData(t *testing.T) {
	if _, err := unboxValue([]byte(`{"json_wrapper":1} junk`)); err == nil {
		t.Error("unboxValue accepted trailing data")
	}
}

// An S3 or bookshelf error body can echo the request's credentials
// (SignatureDoesNotMatch includes the canonical request and the provided
// signature); only its Code and Message belong in an error.
func TestTransferError_KeepsOnlyS3CodeAndMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>SignatureDoesNotMatch</Code><Message>The request signature we calculated does not match the signature you provided.</Message><AWSAccessKeyId>AKIAEXAMPLE</AWSAccessKeyId><CanonicalRequest>GET /x X-Amz-Credential=AKIAEXAMPLE%2Fsecret X-Amz-Security-Token=tok</CanonicalRequest><SignatureProvided>deadbeef</SignatureProvided></Error>`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	recordSleeps(c)
	err := c.Fetch(context.Background(), srv.URL+"/x", func(io.Reader) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch: The request signature") {
		t.Fatalf("err = %v, want the S3 code and message", err)
	}
	for _, secret := range []string{"AKIAEXAMPLE", "Security-Token", "deadbeef"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("err leaks %q: %v", secret, err)
		}
	}
}

// A body that is not a Chef error (a proxy's HTML page) is kept, but capped.
func TestErrorResponse_CapsRawBody(t *testing.T) {
	er := newErrorResponse("GET", "/x", 502, []byte(strings.Repeat("x", 100_000)))
	if n := len(er.ServerMessage()); n > maxRawErrorBody+len("…") {
		t.Errorf("message is %d bytes, want at most %d", n, maxRawErrorBody)
	}
}

// truncate never cuts a multi-byte character in half.
func TestTruncate_RuneBoundary(t *testing.T) {
	if got := truncate("ab€", 3); got != "ab…" { // € is 3 bytes: cutting at 3 would split it
		t.Errorf("truncate = %q, want %q", got, "ab…")
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Errorf("truncate = %q, want it unchanged", got)
	}
}
