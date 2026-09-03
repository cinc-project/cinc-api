package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestOrgs_List(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations", cinctest.Route{
		Body: `{
			"myorg":"http://x/organizations/myorg",
			"other":"http://x/organizations/other"
		}`,
	})
	c := newTestClient(t, srv.Server)

	list, _, err := c.Orgs.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list["myorg"] == "" || list["other"] == "" {
		t.Fatalf("List = %+v", list)
	}
}

func TestOrgs_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/myorg", cinctest.Route{
		Body: `{"name":"myorg","full_name":"My Organization","guid":"abc123"}`,
	})
	c := newTestClient(t, srv.Server)

	o, _, err := c.Orgs.Get(context.Background(), "myorg")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if o.Name != "myorg" || o.FullName != "My Organization" || o.GUID != "abc123" {
		t.Errorf("Org = %+v", o)
	}
}

func TestOrgs_Create_ReturnsValidatorKey(t *testing.T) {
	// Org creation returns the validator client name and its private key.
	// The private key is shown ONLY at creation time — capturing it is the
	// primary reason the caller hits this endpoint.
	srv := cinctest.New(t)
	srv.Handle("POST /organizations", cinctest.Route{
		Status: 201,
		Body: `{
			"uri":"http://x/organizations/myorg",
			"clientname":"myorg-validator",
			"private_key":"-----BEGIN RSA PRIVATE KEY-----\nGEN\n-----END RSA PRIVATE KEY-----\n"
		}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got["name"] != "myorg" || got["full_name"] != "My Organization" {
				t.Errorf("POST body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	res, _, err := c.Orgs.Create(context.Background(), &Org{
		Name: "myorg", FullName: "My Organization",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ClientName != "myorg-validator" {
		t.Errorf("clientname = %q", res.ClientName)
	}
	if res.PrivateKey == "" {
		t.Fatal("expected private_key in create response")
	}
	if res.URI == "" {
		t.Errorf("expected URI")
	}
}

func TestOrgs_Update(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/myorg", cinctest.Route{
		Body: `{"name":"myorg","full_name":"Renamed Org","guid":"abc123"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]any
			json.Unmarshal(body, &got)
			if got["full_name"] != "Renamed Org" {
				t.Errorf("PUT body full_name = %v", got["full_name"])
			}
		},
	})
	c := newTestClient(t, srv.Server)

	updated, _, err := c.Orgs.Update(context.Background(), &Org{
		Name: "myorg", FullName: "Renamed Org",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.FullName != "Renamed Org" {
		t.Errorf("FullName = %q", updated.FullName)
	}
}

func TestOrgs_Delete(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/myorg", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.Orgs.Delete(context.Background(), "myorg"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestOrgs_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/missing", cinctest.Route{
		Status: 404, Body: `{"error":["org not found"]}`,
	})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Orgs.Get(context.Background(), "missing"); err == nil {
		t.Fatal("expected 404")
	}
}

func TestOrgs_PathsAreTopLevel(t *testing.T) {
	// /organizations is NOT org-scoped — must not be prefixed by /organizations/o.
	// If Orgs.List were wrongly routed through orgPath the URL would become
	// /organizations/o/organizations and cinctest would reject it.
	srv := cinctest.New(t)
	srv.Handle("GET /organizations", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Orgs.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}
