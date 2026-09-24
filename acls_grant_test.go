package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// aclBody is a full ACL in which every permission grants only the admins
// group, so any member a test adds or removes is a visible change.
const aclBody = `{
	"create":{"actors":[],"groups":["admins"]},
	"read":{"actors":["alice"],"groups":["admins"]},
	"update":{"actors":[],"groups":["admins"]},
	"delete":{"actors":[],"groups":["admins"]},
	"grant":{"actors":[],"groups":["admins"]}
}`

func TestACLTarget_Paths(t *testing.T) {
	for _, tc := range []struct {
		target ACLTarget
		get    string
		put    string
	}{
		{ObjectACL(ACLNodes, "web01"), "/organizations/o/nodes/web01/_acl", "/organizations/o/nodes/web01/_acl/read"},
		{ObjectACL(ACLDataBags, "secrets"), "/organizations/o/data/secrets/_acl", "/organizations/o/data/secrets/_acl/read"},
		{OrgACL(), "/organizations/o/organizations/_acl", "/organizations/o/organizations/_acl/read"},
		{UserACL("alice"), "/users/alice/_acl", "/users/alice/_acl/read"},
	} {
		t.Run(tc.target.String(), func(t *testing.T) {
			srv := cinctest.New(t)
			srv.Handle("GET "+tc.get, cinctest.Route{Body: aclBody})
			srv.Handle("PUT "+tc.put, cinctest.Route{Body: `{}`})
			c := newTestClient(t, srv.Server)
			ctx := context.Background()
			acl, _, err := c.ACLs.GetTarget(ctx, tc.target)
			if err != nil {
				t.Fatalf("GetTarget: %v", err)
			}
			if !reflect.DeepEqual(acl.Read.Actors, []string{"alice"}) {
				t.Errorf("read.actors = %v", acl.Read.Actors)
			}
			if err := c.ACLs.SetTargetPermission(ctx, tc.target, "read", &acl.Read); err != nil {
				t.Fatalf("SetTargetPermission: %v", err)
			}
		})
	}
}

func TestACLTarget_Accessors(t *testing.T) {
	for _, tc := range []struct {
		target           ACLTarget
		objectType, name string
		str              string
	}{
		{ObjectACL(ACLPolicyGroups, "prod"), "policy_groups", "prod", "policy_groups/prod"},
		{OrgACL(), "organizations", "", "organization"},
		{UserACL("alice"), "users", "alice", "users/alice"},
		{ACLTarget{}, "", "", "(no ACL target)"},
	} {
		if got := tc.target.ObjectType(); got != tc.objectType {
			t.Errorf("%v: ObjectType = %q, want %q", tc.target, got, tc.objectType)
		}
		if got := tc.target.Name(); got != tc.name {
			t.Errorf("%v: Name = %q, want %q", tc.target, got, tc.name)
		}
		if got := tc.target.String(); got != tc.str {
			t.Errorf("String = %q, want %q", got, tc.str)
		}
	}
}

// A target missing its type or name would produce a path like
// /organizations/o/nodes//_acl, which no server routes. Refuse it before
// sending anything.
func TestACLTarget_RejectsIncompleteTargets(t *testing.T) {
	for _, target := range []ACLTarget{
		{},
		ObjectACL("", "web01"),
		ObjectACL(ACLNodes, ""),
		UserACL(""),
	} {
		t.Run(target.String(), func(t *testing.T) {
			srv := cinctest.New(t) // no routes: any request fails the test
			c := newTestClient(t, srv.Server)
			ctx := context.Background()
			if _, _, err := c.ACLs.GetTarget(ctx, target); err == nil {
				t.Error("GetTarget returned nil error")
			}
			if err := c.ACLs.SetTargetPermission(ctx, target, "read", &ACE{}); err == nil {
				t.Error("SetTargetPermission returned nil error")
			}
			if _, err := c.ACLs.Grant(ctx, target, "read", []string{"bob"}, nil); err == nil {
				t.Error("Grant returned nil error")
			}
		})
	}
}

func TestACLObjectTypes(t *testing.T) {
	want := []string{
		"clients", "containers", "cookbooks", "cookbook_artifacts", "data",
		"environments", "groups", "nodes", "policies", "policy_groups", "roles",
	}
	if !reflect.DeepEqual(ACLObjectTypes, want) {
		t.Errorf("ACLObjectTypes = %v, want %v", ACLObjectTypes, want)
	}
}

// putRecorder registers a PUT route per permission, recording which were
// written and with what body.
func putRecorder(srv *cinctest.Server, base string, perms ...string) map[string]ACE {
	got := map[string]ACE{}
	for _, perm := range perms {
		srv.Handle("PUT "+base+"/_acl/"+perm, cinctest.Route{
			Body: `{}`,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
				var req map[string]ACE
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("decode %s: %v", body, err)
				}
				got[perm] = req[perm]
			},
		})
	}
	return got
}

func TestACLs_GrantOne(t *testing.T) {
	const base = "/organizations/o/nodes/web01"
	srv := cinctest.New(t)
	srv.Handle("GET "+base+"/_acl", cinctest.Route{Body: aclBody})
	puts := putRecorder(srv, base, "update")
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "update", []string{"bob"}, []string{"ops"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"update"}) {
		t.Errorf("changed = %v", changed)
	}
	want := ACE{Actors: []string{"bob"}, Groups: []string{"admins", "ops"}}
	if !reflect.DeepEqual(puts["update"], want) {
		t.Errorf("PUT update = %+v, want %+v", puts["update"], want)
	}
}

// "all" reads the ACL once and writes only the permissions the members are
// missing from: alice already has read, so read is not written.
func TestACLs_GrantAllSkipsUnchanged(t *testing.T) {
	const base = "/organizations/o/nodes/web01"
	srv := cinctest.New(t)
	gets := 0
	srv.Handle("GET "+base+"/_acl", cinctest.Route{
		Body:   aclBody,
		Assert: func(*testing.T, *http.Request, []byte) { gets++ },
	})
	puts := putRecorder(srv, base, "create", "update", "delete", "grant")
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "all", []string{"alice"}, nil)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if want := []string{"create", "update", "delete", "grant"}; !reflect.DeepEqual(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
	if len(puts) != 4 {
		t.Errorf("PUTs = %v, want 4", puts)
	}
	if gets != 1 {
		t.Errorf("GETs = %d, want 1", gets)
	}
}

// A grant that changes nothing sends no PUT and reports no change.
func TestACLs_GrantNoChange(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/web01/_acl", cinctest.Route{Body: aclBody})
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "read", []string{"alice"}, []string{"admins"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if changed != nil {
		t.Errorf("changed = %v, want nil", changed)
	}
}

func TestACLs_Revoke(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/organizations/_acl", cinctest.Route{Body: aclBody})
	puts := putRecorder(srv, "/organizations/o/organizations", "read")
	c := newTestClient(t, srv.Server)

	// alice is only on read; the other four are left alone.
	changed, err := c.ACLs.Revoke(context.Background(), OrgACL(), "all", []string{"alice"}, nil)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"read"}) {
		t.Errorf("changed = %v", changed)
	}
	want := ACE{Actors: []string{}, Groups: []string{"admins"}}
	if !reflect.DeepEqual(puts["read"], want) {
		t.Errorf("PUT read = %+v, want %+v", puts["read"], want)
	}
}

// When one write fails, Grant returns the permissions it already changed and
// an error naming the one that failed, which still unwraps to the server's
// response.
func TestACLs_GrantPartialFailure(t *testing.T) {
	const base = "/users/alice"
	srv := cinctest.New(t)
	srv.Handle("GET "+base+"/_acl", cinctest.Route{Body: aclBody})
	putRecorder(srv, base, "create")
	srv.Handle("PUT "+base+"/_acl/read", cinctest.Route{
		Status: 400,
		Body:   `{"error":["The actor(s) ghost do not exist in this organization as clients or users."]}`,
	})
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Grant(context.Background(), UserACL("alice"), "all", []string{"ghost"}, nil)
	if !reflect.DeepEqual(changed, []string{"create"}) {
		t.Errorf("changed = %v, want [create]", changed)
	}
	var ce *ACLChangeError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ACLChangeError", err)
	}
	if ce.Perm != "read" || !reflect.DeepEqual(ce.Changed, []string{"create"}) || ce.Target != UserACL("alice") {
		t.Errorf("ACLChangeError = %+v", ce)
	}
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("errors.Is(err, ErrBadRequest) = false for %v", err)
	}
	for _, want := range []string{"read", "users/alice", "already changed: create", "ghost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Error() = %q, want it to mention %q", err, want)
		}
	}
}

// A failure on the first write names no already-changed permissions.
func TestACLs_RevokeFirstWriteFails(t *testing.T) {
	const base = "/organizations/o/nodes/web01"
	srv := cinctest.New(t)
	srv.Handle("GET "+base+"/_acl", cinctest.Route{Body: aclBody})
	srv.Handle("PUT "+base+"/_acl/grant", cinctest.Route{
		Status: 403,
		Body:   `{"error":["Admin group cannot be removed from the Grant ACE"]}`,
	})
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Revoke(context.Background(), ObjectACL(ACLNodes, "web01"), "grant", nil, []string{"admins"})
	if changed != nil {
		t.Errorf("changed = %v, want nil", changed)
	}
	var ce *ACLChangeError
	if !errors.As(err, &ce) || ce.Perm != "grant" || ce.Changed != nil {
		t.Fatalf("err = %#v, want an ACLChangeError on grant", err)
	}
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("errors.Is(err, ErrForbidden) = false")
	}
	if strings.Contains(err.Error(), "already changed") {
		t.Errorf("Error() = %q mentions changes that were not made", err)
	}
}

// A failed read is returned as is: nothing was changed.
func TestACLs_GrantReadFails(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/missing/_acl", cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "missing"), "read", []string{"bob"}, nil)
	if changed != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("Grant = %v, %v; want nil, ErrNotFound", changed, err)
	}
	var ce *ACLChangeError
	if errors.As(err, &ce) {
		t.Errorf("a failed read should not be an ACLChangeError: %v", err)
	}
}

// Bad arguments are refused before any request.
func TestACLs_GrantRevokeValidateFirst(t *testing.T) {
	srv := cinctest.New(t) // no routes: any request fails the test
	c := newTestClient(t, srv.Server)
	ctx := context.Background()
	target := ObjectACL(ACLNodes, "web01")

	if _, err := c.ACLs.Grant(ctx, target, "bogus", []string{"bob"}, nil); err == nil {
		t.Error("Grant(bogus perm) returned nil error")
	}
	if _, err := c.ACLs.Revoke(ctx, target, "read", nil, nil); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Errorf("Revoke(no members) = %v, want an at-least-one-member error", err)
	}
	if _, err := c.ACLs.Grant(ctx, target, "read", []string{}, []string{}); err == nil {
		t.Error("Grant(empty members) returned nil error")
	}
}
