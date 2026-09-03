package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestPolicies_List(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policies", cinctest.Route{
		Body: `{
			"appserver":{
				"uri":"http://x/policies/appserver",
				"revisions":{"abc123":{},"def456":{}}
			}
		}`,
	})
	c := newTestClient(t, srv.Server)

	list, _, err := c.Policies.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	entry, ok := list["appserver"]
	if !ok {
		t.Fatalf("List missing appserver: %+v", list)
	}
	if entry.URI == "" {
		t.Errorf("URI empty")
	}
	if len(entry.Revisions) != 2 {
		t.Errorf("Revisions = %v, want 2 entries", entry.Revisions)
	}
}

func TestPolicies_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policies/appserver", cinctest.Route{
		Body: `{"revisions":{"abc":{},"def":{}}}`,
	})
	c := newTestClient(t, srv.Server)

	revs, _, err := c.Policies.Get(context.Background(), "appserver")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := revs.Revisions["abc"]; !ok {
		t.Errorf("missing revision abc: %+v", revs.Revisions)
	}
}

func TestPolicies_Delete(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/policies/appserver", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.Policies.Delete(context.Background(), "appserver"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestPolicies_GetRevision(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policies/appserver/revisions/abc", cinctest.Route{
		Body: `{
			"revision_id":"abc",
			"name":"appserver",
			"run_list":["recipe[appserver::default]"],
			"cookbook_locks":{
				"appserver":{
					"version":"1.0.0",
					"identifier":"deadbeef",
					"dotted_decimal_identifier":"123.456.789",
					"source":"./cookbooks/appserver",
					"cache_key":"appserver-1.0.0",
					"source_options":{"path":"./cookbooks/appserver"}
				}
			},
			"default_attributes":{"port":8080},
			"override_attributes":{}
		}`,
	})
	c := newTestClient(t, srv.Server)

	rev, _, err := c.Policies.GetRevision(context.Background(), "appserver", "abc")
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if rev.RevisionID != "abc" || rev.Name != "appserver" {
		t.Errorf("fields = %+v", rev)
	}
	if len(rev.RunList) != 1 || rev.RunList[0] != "recipe[appserver::default]" {
		t.Errorf("RunList = %v", rev.RunList)
	}
	lock, ok := rev.CookbookLocks["appserver"]
	if !ok {
		t.Fatalf("missing cookbook_lock for appserver: %+v", rev.CookbookLocks)
	}
	if lock.Version != "1.0.0" || lock.Identifier != "deadbeef" {
		t.Errorf("lock = %+v", lock)
	}
	if rev.DefaultAttributes["port"] != float64(8080) {
		t.Errorf("default_attributes = %v", rev.DefaultAttributes)
	}
}

func TestPolicies_CreateRevision(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/policies/appserver/revisions", cinctest.Route{
		Status: 201,
		Body:   `{"revision_id":"new1","name":"appserver"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got["revision_id"] != "new1" {
				t.Errorf("revision_id = %v", got["revision_id"])
			}
		},
	})
	c := newTestClient(t, srv.Server)

	doc := map[string]any{
		"revision_id": "new1",
		"name":        "appserver",
		"run_list":    []string{"recipe[appserver]"},
	}
	rev, _, err := c.Policies.CreateRevision(context.Background(), "appserver", doc)
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if rev.RevisionID != "new1" {
		t.Errorf("RevisionID = %q", rev.RevisionID)
	}
}

func TestPolicies_DeleteRevision(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/policies/appserver/revisions/abc",
		cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.Policies.DeleteRevision(context.Background(), "appserver", "abc"); err != nil {
		t.Fatalf("DeleteRevision: %v", err)
	}
}

func TestPolicies_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policies/missing",
		cinctest.Route{Status: 404, Body: `{"error":["policy not found"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Policies.Get(context.Background(), "missing"); err == nil {
		t.Fatal("expected 404")
	}
}

func TestCookbookLock_Origin(t *testing.T) {
	cases := []struct {
		name      string
		opts      map[string]any
		wantKind  SourceKind
		wantValue string
		wantErr   bool
	}{
		{"path", map[string]any{"path": "../base"}, SourcePath, "../base", false},
		{"git", map[string]any{"git": "https://example.test/base.git"}, SourceGit, "https://example.test/base.git", false},
		{"artifactserver", map[string]any{"artifactserver": "https://supermarket.test/api/v1/cookbooks/base/versions/1_0_0/download"}, SourceArtifactserver, "https://supermarket.test/api/v1/cookbooks/base/versions/1_0_0/download", false},
		{"chef_server", map[string]any{"chef_server": "https://chef.test/organizations/o"}, SourceChefServer, "https://chef.test/organizations/o", false},
		{"path wins over artifactserver", map[string]any{"path": "../base", "artifactserver": "https://x/"}, SourcePath, "../base", false},
		{"missing", map[string]any{"version": "1.0.0"}, "", "", true},
		{"empty", map[string]any{}, "", "", true},
		{"non-string value", map[string]any{"git": 42}, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lock := CookbookLock{SourceOptions: tc.opts}
			kind, value, err := lock.Origin()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Origin() = (%q, %q, nil), want error", kind, value)
				}
				return
			}
			if err != nil {
				t.Fatalf("Origin() error = %v", err)
			}
			if kind != tc.wantKind || value != tc.wantValue {
				t.Fatalf("Origin() = (%q, %q), want (%q, %q)", kind, value, tc.wantKind, tc.wantValue)
			}
		})
	}
}

func TestCookbookLock_PinnedVersion(t *testing.T) {
	cases := []struct {
		name string
		lock CookbookLock
		want string
	}{
		{"source_options version wins", CookbookLock{Version: "1.0.0", SourceOptions: map[string]any{"version": "2.0.0"}}, "2.0.0"},
		{"falls back to top-level version", CookbookLock{Version: "1.0.0", SourceOptions: map[string]any{"git": "x"}}, "1.0.0"},
		{"empty source_options version ignored", CookbookLock{Version: "1.0.0", SourceOptions: map[string]any{"version": ""}}, "1.0.0"},
		{"non-string source_options version ignored", CookbookLock{Version: "1.0.0", SourceOptions: map[string]any{"version": 2}}, "1.0.0"},
		{"nil source_options", CookbookLock{Version: "1.0.0"}, "1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.lock.PinnedVersion(); got != tc.want {
				t.Fatalf("PinnedVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}
