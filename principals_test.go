package cinc

import (
	"context"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestPrincipals_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/principals/normal_user", cinctest.Route{
		Body: `{"principals":[{
			"name":"normal_user",
			"type":"user",
			"public_key":"-----BEGIN PUBLIC KEY-----...",
			"authz_id":"eca5fdd45a8b4bacc04bbc6e37a340be",
			"org_member":false
		}]}`,
	})
	c := newTestClient(t, srv.Server)

	ps, _, err := c.Principals.Get(context.Background(), "normal_user")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("Get returned %d principals", len(ps))
	}
	p := ps[0]
	if p.Name != "normal_user" || p.Type != "user" || p.OrgMember {
		t.Fatalf("principal = %+v", p)
	}
	if p.PublicKey == "" || p.AuthzID == "" {
		t.Fatalf("principal = %+v", p)
	}
}

func TestPrincipals_GetNotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/principals/ghost",
		cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Principals.Get(context.Background(), "ghost"); err == nil {
		t.Fatal("expected 404")
	}
}
