package cinc

import (
	"context"
	"net/http"
	"net/http/httptest"
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
