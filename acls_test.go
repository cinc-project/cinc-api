package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// granularACL is web01's ACL as erchef answers GET .../_acl?detail=granular:
// every ACE carries "actors": [] and the names split into "users" and
// "clients" (oc_chef_authz_acl:part_with_actors).
const granularACL = `{
	"create":{"actors":[],"users":["pivotal"],"clients":[],"groups":["admins"]},
	"read":{"actors":[],"users":[],"clients":["web01"],"groups":["admins","users","clients"]},
	"update":{"actors":[],"users":["pivotal"],"clients":[],"groups":["admins"]},
	"delete":{"actors":[],"users":["pivotal"],"clients":[],"groups":["admins"]},
	"grant":{"actors":[],"users":["pivotal"],"clients":[],"groups":["admins"]}
}`

// Get always asks for the granular form, so a user and a client that share a
// name stay distinguishable (oc_chef_wm_acl:to_json reads detail=granular).
func TestACLs_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/web01/_acl", cinctest.Route{
		Body: granularACL,
		Assert: func(t *testing.T, r *http.Request, _ []byte) {
			if got := r.URL.RawQuery; got != "detail=granular" {
				t.Errorf("query = %q, want detail=granular", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	acl, resp, err := c.ACLs.Get(context.Background(), ObjectACL(ACLNodes, "web01"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp == nil {
		t.Error("Get returned a nil *Response")
	}
	want := ACE{Actors: []string{}, Users: []string{"pivotal"}, Clients: []string{}, Groups: []string{"admins"}}
	if !reflect.DeepEqual(acl.Create, want) {
		t.Errorf("create = %+v, want %+v", acl.Create, want)
	}
	if !reflect.DeepEqual(acl.Read.Clients, []string{"web01"}) || len(acl.Read.Users) != 0 {
		t.Errorf("read = %+v", acl.Read)
	}
}

// A server that ignores detail=granular answers with the flat form; the names
// land in Actors.
func TestACLs_GetFlatForm(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/janedoe/_acl", cinctest.Route{
		Body: `{"update":{"actors":["janedoe"],"groups":[]}}`,
	})
	c := newTestClient(t, srv.Server)

	acl, _, err := c.ACLs.Get(context.Background(), UserACL("janedoe"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(acl.Update.Actors, []string{"janedoe"}) || acl.Update.Users != nil {
		t.Errorf("update = %+v", acl.Update)
	}
}

// putBody registers a PUT route at path and stores its decoded body in got.
func putBody(srv *cinctest.Server, path string, got *map[string]map[string]json.RawMessage) {
	srv.Handle("PUT "+path, cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			if err := json.Unmarshal(body, got); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
		},
	})
}

// rawJSON renders an ACE body's fields as compact JSON for comparison.
func rawJSON(m map[string]json.RawMessage) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = string(v)
	}
	return out
}

// An ACE with no users or clients is written in the flat form erchef has
// always accepted: {"<perm>": {"actors": [...], "groups": [...]}}, with nil
// lists sent as [] (acl_spec requires both keys as arrays).
func TestACLs_SetPermissionFlatForm(t *testing.T) {
	srv := cinctest.New(t)
	var got map[string]map[string]json.RawMessage
	putBody(srv, "/organizations/o/nodes/web01/_acl/update", &got)
	c := newTestClient(t, srv.Server)

	resp, err := c.ACLs.SetPermission(context.Background(), ObjectACL(ACLNodes, "web01"), "update", &ACE{
		Groups: []string{"admins", "ops"},
	})
	if err != nil {
		t.Fatalf("SetPermission: %v", err)
	}
	if resp == nil {
		t.Error("SetPermission returned a nil *Response")
	}
	want := map[string]string{"actors": `[]`, "groups": `["admins","ops"]`}
	if !reflect.DeepEqual(rawJSON(got["update"]), want) {
		t.Errorf("PUT update = %v, want %v", rawJSON(got["update"]), want)
	}
}

// An ACE with users or clients is written in the granular form. erchef's
// acl_spec requires "actors" and "groups" on every write, and
// validate_actors_clients_users requires "users" and "clients" together with
// "actors": [] (oc_chef_authz_acl.erl:51-90), so all four keys are sent.
func TestACLs_SetPermissionGranularForm(t *testing.T) {
	for _, tc := range []struct {
		name string
		ace  ACE
		want map[string]string
	}{
		{"users only", ACE{Users: []string{"jenkins"}},
			map[string]string{"actors": `[]`, "users": `["jenkins"]`, "clients": `[]`, "groups": `[]`}},
		{"clients only", ACE{Clients: []string{"jenkins"}, Groups: []string{"admins"}},
			map[string]string{"actors": `[]`, "users": `[]`, "clients": `["jenkins"]`, "groups": `["admins"]`}},
		{"both", ACE{Actors: []string{}, Users: []string{"jenkins"}, Clients: []string{"jenkins"}},
			map[string]string{"actors": `[]`, "users": `["jenkins"]`, "clients": `["jenkins"]`, "groups": `[]`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := cinctest.New(t)
			var got map[string]map[string]json.RawMessage
			putBody(srv, "/organizations/o/organizations/_acl/read", &got)
			c := newTestClient(t, srv.Server)
			if _, err := c.ACLs.SetPermission(context.Background(), OrgACL(), "read", &tc.ace); err != nil {
				t.Fatalf("SetPermission: %v", err)
			}
			if !reflect.DeepEqual(rawJSON(got["read"]), tc.want) {
				t.Errorf("PUT read = %v, want %v", rawJSON(got["read"]), tc.want)
			}
		})
	}
}

// erchef refuses an ACE with names in both actors and users/clients
// (actors_must_be_empty, a 400). Refuse it before sending, and refuse a nil
// ACE rather than panicking.
func TestACLs_SetPermissionRejectsBadACE(t *testing.T) {
	srv := cinctest.New(t) // no routes: any request fails the test
	c := newTestClient(t, srv.Server)
	ctx := context.Background()
	_, err := c.ACLs.SetPermission(ctx, ObjectACL(ACLNodes, "n1"), "read",
		&ACE{Actors: []string{"alice"}, Users: []string{"bob"}})
	if err == nil || !strings.Contains(err.Error(), "Actors") {
		t.Errorf("mixed ACE: err = %v, want an error naming Actors", err)
	}
	if _, err := c.ACLs.SetPermission(ctx, ObjectACL(ACLNodes, "n1"), "read", nil); err == nil {
		t.Error("nil ACE: no error")
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
		{ACLClients, "c1"}, {ACLGroups, "devs"},
		{ACLPolicies, "appserver"}, {ACLContainers, "nodes"},
	} {
		if _, _, err := c.ACLs.Get(ctx, ObjectACL(tc.kind, tc.name)); err != nil {
			t.Errorf("Get(%s, %s): %v", tc.kind, tc.name, err)
		}
	}
}

// The org name comes from Config and must be escaped on both the read and the
// write path, with the signature covering exactly what goes on the wire.
func TestACLs_OrgACLEscapesOrgName(t *testing.T) {
	key := testRSAKey(t)
	var wire []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifySignature(t, r, key)
		wire = append(wire, r.Method+" "+r.URL.EscapedPath())
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{ServerURL: srv.URL, Org: "a b/c", ClientName: "c", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := c.ACLs.Get(ctx, OrgACL()); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.ACLs.SetPermission(ctx, OrgACL(), "read", &ACE{}); err != nil {
		t.Fatalf("SetPermission: %v", err)
	}
	want := []string{
		"GET /organizations/a%20b%2Fc/organizations/_acl",
		"PUT /organizations/a%20b%2Fc/organizations/_acl/read",
	}
	if !reflect.DeepEqual(wire, want) {
		t.Errorf("wire = %q, want %q", wire, want)
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
	// The returned slice must be independent of the package's list.
	all[0] = "mutated"
	if again, _ := ExpandPerm("all"); again[0] != "create" {
		t.Errorf("ExpandPerm(all) aliased its list: %v", again)
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

// ACLPerms hands out a fresh copy: a caller that edits it changes nothing
// else, neither later calls nor ExpandPerm.
func TestACLPerms_IsACopy(t *testing.T) {
	want := []string{"create", "read", "update", "delete", "grant"}
	p := ACLPerms()
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("ACLPerms() = %v, want %v", p, want)
	}
	p[0] = "x"
	if got := ACLPerms(); !reflect.DeepEqual(got, want) {
		t.Errorf("after editing a copy, ACLPerms() = %v", got)
	}
	if _, err := ExpandPerm("create"); err != nil {
		t.Errorf("after editing a copy, ExpandPerm(create) = %v", err)
	}
	if _, err := ExpandPerm("x"); err == nil {
		t.Error(`after editing a copy, ExpandPerm("x") succeeded`)
	}
}

func TestACL_ACEFor(t *testing.T) {
	acl := &ACL{}
	for _, perm := range ACLPerms() {
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
	ace := &ACE{Users: []string{"alice"}, Groups: []string{"admins"}}

	// Adding a brand-new user, client and group changes the ACE. A client
	// named like an existing user is a different member.
	if !ace.AddMembers(ACLMembers{Users: []string{"bob"}, Clients: []string{"alice"}, Groups: []string{"ops"}}) {
		t.Fatal("AddMembers should report a change")
	}
	want := &ACE{Users: []string{"alice", "bob"}, Clients: []string{"alice"}, Groups: []string{"admins", "ops"}}
	if !reflect.DeepEqual(ace, want) {
		t.Errorf("ace = %+v, want %+v", ace, want)
	}

	// Re-adding existing members is a no-op (deduped).
	if ace.AddMembers(ACLMembers{Users: []string{"alice", "bob"}, Clients: []string{"alice"}, Groups: []string{"admins"}}) {
		t.Error("AddMembers of existing members should report no change")
	}
	if !reflect.DeepEqual(ace, want) {
		t.Errorf("ace after no-op add = %+v", ace)
	}
}

func TestACE_RemoveMembers(t *testing.T) {
	ace := &ACE{Users: []string{"alice", "bob"}, Clients: []string{"bob"}, Groups: []string{"admins", "ops"}}

	// Removing client bob leaves user bob.
	if !ace.RemoveMembers(ACLMembers{Clients: []string{"bob"}, Groups: []string{"ops"}}) {
		t.Fatal("RemoveMembers should report a change")
	}
	want := &ACE{Users: []string{"alice", "bob"}, Clients: []string{}, Groups: []string{"admins"}}
	if !reflect.DeepEqual(ace, want) {
		t.Errorf("ace = %+v, want %+v", ace, want)
	}
	if !ace.RemoveMembers(ACLMembers{Users: []string{"bob"}}) {
		t.Fatal("RemoveMembers(user bob) should report a change")
	}

	// Removing absent members is a no-op.
	if ace.RemoveMembers(ACLMembers{Users: []string{"carol"}, Clients: []string{"alice"}, Groups: []string{"temps"}}) {
		t.Error("RemoveMembers of absent members should report no change")
	}
}

// A list the server returned with a repeated name must lose every copy, or a
// revoke leaves the member granted.
func TestACE_RemoveMembers_RemovesEveryCopy(t *testing.T) {
	a := &ACE{Users: []string{"alice", "bob", "alice"}, Groups: []string{}}
	if !a.RemoveMembers(ACLMembers{Users: []string{"alice"}}) {
		t.Fatal("RemoveMembers reported no change")
	}
	if len(a.Users) != 1 || a.Users[0] != "bob" {
		t.Errorf("users = %v, want [bob]", a.Users)
	}
}

func TestACLs_GetNotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/missing/_acl",
		cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.ACLs.Get(context.Background(), ObjectACL(ACLNodes, "missing")); err == nil {
		t.Fatal("expected 404")
	}
}

// "all" is a pseudo-permission for ExpandPerm, not an endpoint. Sending it
// produced PUT .../_acl/all, which is not a thing the server serves.
func TestSetPermission_RejectsInvalidPerm(t *testing.T) {
	for _, perm := range []string{"all", "", "READ", "destroy"} {
		t.Run(perm, func(t *testing.T) {
			srv := cinctest.New(t) // any request fails the test: no routes registered
			c := newTestClient(t, srv.Server)
			for _, target := range []ACLTarget{ObjectACL(ACLNodes, "n1"), OrgACL(), UserACL("u")} {
				_, err := c.ACLs.SetPermission(context.Background(), target, perm,
					&ACE{Groups: []string{"admins"}})
				if err == nil {
					t.Fatalf("SetPermission(%v, %q) returned nil error", target, perm)
				}
				if !strings.Contains(err.Error(), "unknown permission") {
					t.Errorf("err = %v, want an unknown-permission error", err)
				}
			}
		})
	}
}

// Every standard permission is still accepted.
func TestSetPermission_AcceptsEveryStandardPerm(t *testing.T) {
	for _, perm := range ACLPerms() {
		t.Run(perm, func(t *testing.T) {
			srv := cinctest.New(t)
			srv.Handle("PUT /organizations/o/nodes/n1/_acl/"+perm, cinctest.Route{Body: `{}`})
			c := newTestClient(t, srv.Server)
			if _, err := c.ACLs.SetPermission(context.Background(), ObjectACL(ACLNodes, "n1"), perm, &ACE{}); err != nil {
				t.Errorf("SetPermission(%q): %v", perm, err)
			}
		})
	}
}

func TestUnknownPermErrors(t *testing.T) {
	_, err := ExpandPerm("destroy")
	if err == nil || err.Error() != `cinc: unknown permission "destroy": want one of create, read, update, delete, grant, or all` {
		t.Errorf("ExpandPerm error = %v", err)
	}
	_, err = (&ACL{}).ACEFor("all")
	if err == nil || err.Error() != `cinc: unknown permission "all": want one of create, read, update, delete, or grant (expand "all" with ExpandPerm)` {
		t.Errorf("ACEFor error = %v", err)
	}
}
