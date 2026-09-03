package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestACLs_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/web01/_acl", cinctest.Route{
		Body: `{
			"create":{"actors":["pivotal"],"groups":["admins"]},
			"read":{"actors":[],"groups":["admins","users","clients"]},
			"update":{"actors":["pivotal"],"groups":["admins"]},
			"delete":{"actors":["pivotal"],"groups":["admins"]},
			"grant":{"actors":["pivotal"],"groups":["admins"]}
		}`,
	})
	c := newTestClient(t, srv.Server)

	acl, _, err := c.ACLs.Get(context.Background(), "nodes", "web01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(acl.Create.Actors, []string{"pivotal"}) {
		t.Errorf("create.actors = %v", acl.Create.Actors)
	}
	if !reflect.DeepEqual(acl.Read.Groups, []string{"admins", "users", "clients"}) {
		t.Errorf("read.groups = %v", acl.Read.Groups)
	}
}

func TestACLs_SetPermission(t *testing.T) {
	// PUT a single permission. Body shape: {"<perm>": {"actors": [...], "groups": [...]}}.
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/nodes/web01/_acl/update", cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req map[string]ACE
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, body)
			}
			ace, ok := req["update"]
			if !ok {
				t.Fatalf("PUT body missing perm key: %s", body)
			}
			if !reflect.DeepEqual(ace.Groups, []string{"admins", "ops"}) {
				t.Errorf("groups = %v", ace.Groups)
			}
			// Empty actors must serialize as [] not null.
			var raw struct {
				Update struct {
					Actors json.RawMessage `json:"actors"`
				} `json:"update"`
			}
			json.Unmarshal(body, &raw)
			if string(raw.Update.Actors) != "[]" {
				t.Errorf("actors = %s, want []", raw.Update.Actors)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	err := c.ACLs.SetPermission(context.Background(), "nodes", "web01", "update", &ACE{
		Groups: []string{"admins", "ops"},
	})
	if err != nil {
		t.Fatalf("SetPermission: %v", err)
	}
}

func TestACLs_AcceptsAnyObjectType(t *testing.T) {
	// The same endpoint shape is reachable for every object kind.
	srv := cinctest.New(t)
	for _, path := range []string{
		"GET /organizations/o/clients/c1/_acl",
		"GET /organizations/o/groups/devs/_acl",
		"GET /organizations/o/policies/appserver/_acl",
		"GET /organizations/o/containers/nodes/_acl",
	} {
		srv.Handle(path, cinctest.Route{Body: `{}`})
	}
	c := newTestClient(t, srv.Server)
	ctx := context.Background()
	for _, tc := range []struct{ kind, name string }{
		{"clients", "c1"}, {"groups", "devs"},
		{"policies", "appserver"}, {"containers", "nodes"},
	} {
		if _, _, err := c.ACLs.Get(ctx, tc.kind, tc.name); err != nil {
			t.Errorf("Get(%s, %s): %v", tc.kind, tc.name, err)
		}
	}
}

func TestACLs_GetOrg(t *testing.T) {
	// The organization object's own ACL lives at /organizations/ORG/_acl,
	// with no object-type segment.
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/_acl", cinctest.Route{
		Body: `{"read":{"actors":[],"groups":["admins"]}}`,
	})
	c := newTestClient(t, srv.Server)

	acl, _, err := c.ACLs.GetOrg(context.Background())
	if err != nil {
		t.Fatalf("GetOrg: %v", err)
	}
	if !reflect.DeepEqual(acl.Read.Groups, []string{"admins"}) {
		t.Errorf("read.groups = %v", acl.Read.Groups)
	}
}

func TestACLs_SetOrgPermission(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/_acl/grant", cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req map[string]ACE
			json.Unmarshal(body, &req)
			if !reflect.DeepEqual(req["grant"].Groups, []string{"admins"}) {
				t.Errorf("PUT body = %s", body)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if err := c.ACLs.SetOrgPermission(context.Background(), "grant", &ACE{
		Groups: []string{"admins"},
	}); err != nil {
		t.Fatalf("SetOrgPermission: %v", err)
	}
}

func TestACLs_GetUser(t *testing.T) {
	// A global user's ACL is top-level, not org-scoped.
	srv := cinctest.New(t)
	srv.Handle("GET /users/janedoe/_acl", cinctest.Route{
		Body: `{"update":{"actors":["janedoe"],"groups":[]}}`,
	})
	c := newTestClient(t, srv.Server)

	acl, _, err := c.ACLs.GetUser(context.Background(), "janedoe")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !reflect.DeepEqual(acl.Update.Actors, []string{"janedoe"}) {
		t.Errorf("update.actors = %v", acl.Update.Actors)
	}
}

func TestACLs_SetUserPermission(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /users/janedoe/_acl/grant", cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req map[string]ACE
			json.Unmarshal(body, &req)
			if !reflect.DeepEqual(req["grant"].Actors, []string{"janedoe"}) {
				t.Errorf("PUT body = %s", body)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if err := c.ACLs.SetUserPermission(context.Background(), "janedoe", "grant", &ACE{
		Actors: []string{"janedoe"},
	}); err != nil {
		t.Fatalf("SetUserPermission: %v", err)
	}
}

func TestExpandPerm(t *testing.T) {
	all, err := ExpandPerm("all")
	if err != nil {
		t.Fatalf("ExpandPerm(all): %v", err)
	}
	if !reflect.DeepEqual(all, []string{"create", "read", "update", "delete", "grant"}) {
		t.Errorf("ExpandPerm(all) = %v", all)
	}
	// The returned slice must be independent of the package var.
	all[0] = "mutated"
	if ACLPerms[0] != "create" {
		t.Errorf("ExpandPerm(all) aliased ACLPerms: %v", ACLPerms)
	}

	one, err := ExpandPerm("read")
	if err != nil {
		t.Fatalf("ExpandPerm(read): %v", err)
	}
	if !reflect.DeepEqual(one, []string{"read"}) {
		t.Errorf("ExpandPerm(read) = %v", one)
	}

	if _, err := ExpandPerm("bogus"); err == nil {
		t.Error("ExpandPerm(bogus) should error")
	}
}

func TestACL_ACEFor(t *testing.T) {
	acl := &ACL{}
	for _, perm := range ACLPerms {
		ace, err := acl.ACEFor(perm)
		if err != nil {
			t.Fatalf("ACEFor(%s): %v", perm, err)
		}
		// The pointer must alias the struct field so mutations persist.
		ace.Groups = append(ace.Groups, perm)
	}
	if !reflect.DeepEqual(acl.Create.Groups, []string{"create"}) ||
		!reflect.DeepEqual(acl.Grant.Groups, []string{"grant"}) {
		t.Errorf("ACEFor did not return field pointers: %+v", acl)
	}
	if _, err := acl.ACEFor("all"); err == nil {
		t.Error(`ACEFor("all") should error — "all" is not a single ACE`)
	}
	if _, err := acl.ACEFor("bogus"); err == nil {
		t.Error("ACEFor(bogus) should error")
	}
}

func TestACE_AddMembers(t *testing.T) {
	ace := &ACE{Actors: []string{"alice"}, Groups: []string{"admins"}}

	// Adding a brand-new actor and group changes the ACE.
	if !ace.AddMembers([]string{"bob"}, []string{"ops"}) {
		t.Fatal("AddMembers should report a change")
	}
	if !reflect.DeepEqual(ace.Actors, []string{"alice", "bob"}) {
		t.Errorf("actors = %v", ace.Actors)
	}
	if !reflect.DeepEqual(ace.Groups, []string{"admins", "ops"}) {
		t.Errorf("groups = %v", ace.Groups)
	}

	// Re-adding existing members is a no-op (deduped).
	if ace.AddMembers([]string{"alice", "bob"}, []string{"admins"}) {
		t.Error("AddMembers of existing members should report no change")
	}
	if !reflect.DeepEqual(ace.Actors, []string{"alice", "bob"}) {
		t.Errorf("actors after no-op add = %v", ace.Actors)
	}
}

func TestACE_RemoveMembers(t *testing.T) {
	ace := &ACE{Actors: []string{"alice", "bob"}, Groups: []string{"admins", "ops"}}

	if !ace.RemoveMembers([]string{"bob"}, []string{"ops"}) {
		t.Fatal("RemoveMembers should report a change")
	}
	if !reflect.DeepEqual(ace.Actors, []string{"alice"}) {
		t.Errorf("actors = %v", ace.Actors)
	}
	if !reflect.DeepEqual(ace.Groups, []string{"admins"}) {
		t.Errorf("groups = %v", ace.Groups)
	}

	// Removing absent members is a no-op.
	if ace.RemoveMembers([]string{"carol"}, []string{"temps"}) {
		t.Error("RemoveMembers of absent members should report no change")
	}
}

func TestACLs_GetNotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/missing/_acl",
		cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.ACLs.Get(context.Background(), "nodes", "missing"); err == nil {
		t.Fatal("expected 404")
	}
}
