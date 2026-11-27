package cinc

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// A cookbook or artifact manifest is fetched at server API version 2. At 0
// and 1 erchef rebuilds the nine classic segments from the stored all_files
// and drops every file outside them (ohai/, compliance/, spec/, ...), so a
// download would silently miss those files. The cinctest harness re-checks
// the signature against the version header, so this also proves the request
// is signed for version 2.
func TestManifestGet_AsksForServerAPIVersion2(t *testing.T) {
	const manifest = `{"cookbook_name":"nginx","version":"1.2.0","name":"nginx-1.2.0",
		"all_files":[{"name":"ohai/plugins/x.rb","path":"ohai/plugins/x.rb","checksum":"","url":"http://x"}]}`
	assertV2 := func(t *testing.T, r *http.Request, _ []byte) {
		if v := r.Header.Get("X-Ops-Server-API-Version"); v != "2" {
			t.Errorf("manifest GET API version = %q, want 2", v)
		}
	}
	for _, tc := range []struct {
		desc, route string
		get         func(*Client) (*Cookbook, *Response, error)
	}{
		{"cookbook", "GET /organizations/o/cookbooks/nginx/1.2.0",
			func(c *Client) (*Cookbook, *Response, error) {
				return c.Cookbooks.Get(context.Background(), "nginx", "1.2.0")
			}},
		{"artifact", "GET /organizations/o/cookbook_artifacts/nginx/abc123",
			func(c *Client) (*Cookbook, *Response, error) {
				return c.CookbookArtifacts.Get(context.Background(), "nginx", "abc123")
			}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			srv := cinctest.New(t)
			srv.Handle(tc.route, cinctest.Route{Body: manifest, Assert: assertV2})
			cb, _, err := tc.get(newTestClient(t, srv.Server))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if files := cb.AllFiles(); len(files) != 1 || files[0].Path != "ohai/plugins/x.rb" {
				t.Fatalf("AllFiles = %+v, want the ohai plugin", files)
			}
		})
	}
}

// Download fetches its manifest through Get, so it asks for version 2 too.
func TestCookbooksDownload_AsksForServerAPIVersion2(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/nginx/1.2.0", cinctest.Route{
		Body: `{"cookbook_name":"nginx","version":"1.2.0","all_files":[]}`,
		Assert: func(t *testing.T, r *http.Request, _ []byte) {
			if v := r.Header.Get("X-Ops-Server-API-Version"); v != "2" {
				t.Errorf("Download manifest GET API version = %q, want 2", v)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if err := c.Cookbooks.Download(context.Background(), "nginx", "1.2.0", filepath.Join(t.TempDir(), "out")); err != nil {
		t.Fatalf("Download: %v", err)
	}
}
