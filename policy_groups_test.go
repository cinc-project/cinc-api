package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestPolicyGroups_List(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policy_groups", cinctest.Route{
		Body: `{
			"production":{
				"uri":"http://x/policy_groups/production",
				"policies":{"appserver":{"revision_id":"abc"}}
			}
		}`,
	})
	c := newTestClient(t, srv.Server)

	list, _, err := c.PolicyGroups.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	prod, ok := list["production"]
	if !ok {
		t.Fatalf("missing production: %+v", list)
	}
	if prod.URI == "" || prod.Policies["appserver"].RevisionID != "abc" {
		t.Errorf("prod = %+v", prod)
	}
}

func TestPolicyGroups_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policy_groups/production", cinctest.Route{
		Body: `{"uri":"http://x","policies":{"appserver":{"revision_id":"abc"}}}`,
	})
	c := newTestClient(t, srv.Server)
	g, _, err := c.PolicyGroups.Get(context.Background(), "production")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.Policies["appserver"].RevisionID != "abc" {
		t.Errorf("Policies = %+v", g.Policies)
	}
}

func TestPolicyGroups_Delete(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/policy_groups/production", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.PolicyGroups.Delete(context.Background(), "production"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestPolicyGroups_GetPolicy(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policy_groups/production/policies/appserver",
		cinctest.Route{Body: `{"revision_id":"abc","name":"appserver","run_list":["recipe[appserver]"]}`})
	c := newTestClient(t, srv.Server)
	rev, _, err := c.PolicyGroups.GetPolicy(context.Background(), "production", "appserver")
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if rev.RevisionID != "abc" {
		t.Errorf("revision_id = %q", rev.RevisionID)
	}
}

func TestPolicyGroups_PutPolicy(t *testing.T) {
	// PUT to /policy_groups/G/policies/N uploads a revision AND associates
	// it with the group. The body is the full Policyfile document.
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/policy_groups/production/policies/appserver",
		cinctest.Route{
			Body: `{"revision_id":"new1","name":"appserver"}`,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
				var got map[string]any
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got["revision_id"] != "new1" || got["name"] != "appserver" {
					t.Errorf("PUT body = %v", got)
				}
			},
		})
	c := newTestClient(t, srv.Server)
	rev, _, err := c.PolicyGroups.PutPolicy(context.Background(), "production", "appserver",
		&PolicyRevision{RevisionID: "new1", Name: "appserver", RunList: []string{"recipe[appserver]"}})
	if err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	if rev.RevisionID != "new1" {
		t.Errorf("returned revision = %+v", rev)
	}
}

func TestPolicyGroups_DeletePolicy(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/policy_groups/production/policies/appserver",
		cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.PolicyGroups.DeletePolicy(context.Background(), "production", "appserver"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
}

func TestPolicyGroups_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/policy_groups/missing/policies/x",
		cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.PolicyGroups.GetPolicy(context.Background(), "missing", "x"); err == nil {
		t.Fatal("expected 404")
	}
}
