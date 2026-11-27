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

// aclBody is a full ACL, in the granular form Get asks for, in which every
// permission grants only the admins group and read also grants user alice, so
// any member a test adds or removes is a visible change.
const aclBody = `{
	"create":{"actors":[],"users":[],"clients":[],"groups":["admins"]},
	"read":{"actors":[],"users":["alice"],"clients":[],"groups":["admins"]},
	"update":{"actors":[],"users":[],"clients":[],"groups":["admins"]},
	"delete":{"actors":[],"users":[],"clients":[],"groups":["admins"]},
	"grant":{"actors":[],"users":[],"clients":[],"groups":["admins"]}
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
			acl, _, err := c.ACLs.Get(ctx, tc.target)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !reflect.DeepEqual(acl.Read.Users, []string{"alice"}) {
				t.Errorf("read.users = %v", acl.Read.Users)
			}
			if _, err := c.ACLs.SetPermission(ctx, tc.target, "read", &acl.Read); err != nil {
				t.Fatalf("SetPermission: %v", err)
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
			if _, _, err := c.ACLs.Get(ctx, target); err == nil {
				t.Error("Get returned nil error")
			}
			if _, err := c.ACLs.SetPermission(ctx, target, "read", &ACE{}); err == nil {
				t.Error("SetPermission returned nil error")
			}
			if _, err := c.ACLs.Grant(ctx, target, "read", ACLMembers{Users: []string{"bob"}}); err == nil {
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
	got := ACLObjectTypes()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ACLObjectTypes() = %v, want %v", got, want)
	}
	// It is a fresh copy every time.
	got[0] = "x"
	if again := ACLObjectTypes(); !reflect.DeepEqual(again, want) {
		t.Errorf("after editing a copy, ACLObjectTypes() = %v", again)
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

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "update",
		ACLMembers{Users: []string{"bob"}, Groups: []string{"ops"}})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"update"}) {
		t.Errorf("changed = %v", changed)
	}
	want := ACE{Actors: []string{}, Users: []string{"bob"}, Clients: []string{}, Groups: []string{"admins", "ops"}}
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

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "all", ACLMembers{Users: []string{"alice"}})
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

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "read",
		ACLMembers{Users: []string{"alice"}, Groups: []string{"admins"}})
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
	changed, err := c.ACLs.Revoke(context.Background(), OrgACL(), "all", ACLMembers{Users: []string{"alice"}})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"read"}) {
		t.Errorf("changed = %v", changed)
	}
	// The last user is gone, so the ACE goes back in the flat form.
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

	changed, err := c.ACLs.Grant(context.Background(), UserACL("alice"), "all", ACLMembers{Users: []string{"ghost"}})
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

	changed, err := c.ACLs.Revoke(context.Background(), ObjectACL(ACLNodes, "web01"), "grant", ACLMembers{Groups: []string{"admins"}})
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

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "missing"), "read", ACLMembers{Users: []string{"bob"}})
	if changed != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("Grant = %v, %v; want nil, ErrNotFound", changed, err)
	}
	if _, ok := errors.AsType[*ACLChangeError](err); ok {
		t.Errorf("a failed read should not be an ACLChangeError: %v", err)
	}
}

// Bad arguments are refused before any request.
func TestACLs_GrantRevokeValidateFirst(t *testing.T) {
	srv := cinctest.New(t) // no routes: any request fails the test
	c := newTestClient(t, srv.Server)
	ctx := context.Background()
	target := ObjectACL(ACLNodes, "web01")

	if _, err := c.ACLs.Grant(ctx, target, "bogus", ACLMembers{Users: []string{"bob"}}); err == nil {
		t.Error("Grant(bogus perm) returned nil error")
	}
	if _, err := c.ACLs.Revoke(ctx, target, "read", ACLMembers{}); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Errorf("Revoke(no members) = %v, want an at-least-one-member error", err)
	}
	if _, err := c.ACLs.Grant(ctx, target, "read", ACLMembers{Users: []string{}, Clients: []string{}, Groups: []string{}}); err == nil {
		t.Error("Grant(empty members) returned nil error")
	}
}

// refusingServer serves acl as web01's ACL and answers the PUT of failPerm
// with status; every other PUT succeeds.
func refusingServer(t *testing.T, acl, failPerm string, status int) *Client {
	t.Helper()
	const base = "/organizations/o/nodes/web01"
	srv := cinctest.New(t)
	srv.Handle("GET "+base+"/_acl", cinctest.Route{Body: acl})
	for _, p := range ACLPerms() {
		r := cinctest.Route{Body: `{}`}
		if p == failPerm {
			r = cinctest.Route{Status: status, Body: `{"error":["refused"]}`}
		}
		srv.Handle("PUT "+base+"/_acl/"+p, r)
	}
	return newTestClient(t, srv.Server)
}

// erchef refuses to take admins off grant for anyone but the superuser, with
// a 403 that is not about the caller's permissions. Revoke reports it as
// ErrAdminsRequiredOnGrant, still wrapping ErrForbidden and the server error.
func TestACLs_RevokeAdminsFromGrantIsAdminsRequiredOnGrant(t *testing.T) {
	for _, perm := range []string{"grant", "all"} {
		c := refusingServer(t, aclBody, "grant", 403)
		_, err := c.ACLs.Revoke(context.Background(), ObjectACL(ACLNodes, "web01"), perm, ACLMembers{Groups: []string{"admins"}})
		if !errors.Is(err, ErrAdminsRequiredOnGrant) {
			t.Errorf("Revoke(%s): errors.Is(err, ErrAdminsRequiredOnGrant) = false: %v", perm, err)
		}
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("Revoke(%s): errors.Is(err, ErrForbidden) = false", perm)
		}
		var er *ErrorResponse
		if !errors.As(err, &er) || er.StatusCode != 403 {
			t.Errorf("Revoke(%s): errors.As ErrorResponse = %+v", perm, er)
		}
		var ce *ACLChangeError
		if !errors.As(err, &ce) || ce.Perm != "grant" {
			t.Errorf("Revoke(%s): err = %#v, want an ACLChangeError on grant", perm, err)
		}
	}
}

// Only a 403 for a write that dropped admins from grant is that refusal.
func TestACLs_OtherFailuresAreNotAdminsRequiredOnGrant(t *testing.T) {
	const withDevs = `{
	"create":{"actors":[],"groups":["admins"]},
	"read":{"actors":[],"groups":["admins"]},
	"update":{"actors":[],"groups":["admins"]},
	"delete":{"actors":[],"groups":["admins"]},
	"grant":{"actors":[],"users":[],"clients":["bob"],"groups":["admins","devs"]}
}`
	ctx := context.Background()
	target := ObjectACL(ACLNodes, "web01")
	for _, tc := range []struct {
		name     string
		failPerm string
		status   int
		revoke   bool
		members  ACLMembers
	}{
		{"revoke admins from grant, 400", "grant", 400, true, ACLMembers{Groups: []string{"admins"}}},
		{"revoke admins from read, 403", "read", 403, true, ACLMembers{Groups: []string{"admins"}}},
		{"revoke devs from grant, 403", "grant", 403, true, ACLMembers{Groups: []string{"devs"}}},
		{"revoke bob from grant, 403", "grant", 403, true, ACLMembers{Clients: []string{"bob"}}},
		{"grant carol on grant, 403", "grant", 403, false, ACLMembers{Users: []string{"carol"}}},
	} {
		c := refusingServer(t, withDevs, tc.failPerm, tc.status)
		var err error
		if tc.revoke {
			_, err = c.ACLs.Revoke(ctx, target, tc.failPerm, tc.members)
		} else {
			_, err = c.ACLs.Grant(ctx, target, tc.failPerm, tc.members)
		}
		if err == nil {
			t.Errorf("%s: no error; the case sent nothing", tc.name)
			continue
		}
		if errors.Is(err, ErrAdminsRequiredOnGrant) {
			t.Errorf("%s: wrongly reported as ErrAdminsRequiredOnGrant: %v", tc.name, err)
		}
	}
}

// sharedNameACL holds a user and a client both named jenkins on read, as
// erchef reports them with detail=granular.
const sharedNameACL = `{
	"create":{"actors":[],"users":[],"clients":[],"groups":["admins"]},
	"read":{"actors":[],"users":["jenkins"],"clients":["jenkins"],"groups":["admins"]},
	"update":{"actors":[],"users":[],"clients":[],"groups":["admins"]},
	"delete":{"actors":[],"users":[],"clients":[],"groups":["admins"]},
	"grant":{"actors":[],"users":[],"clients":[],"groups":["admins"]}
}`

// A user and a client that share a name and both hold read used to make every
// later write resend "actors":["jenkins","jenkins"], which erchef refuses for
// good with a 422 ambiguous_actor (oc_chef_authz_acl:fetch_actors). Writes now
// keep them apart in "users" and "clients".
func TestACLs_GrantKeepsSameNamedUserAndClientApart(t *testing.T) {
	const base = "/organizations/o/nodes/web01"
	srv := cinctest.New(t)
	srv.Handle("GET "+base+"/_acl", cinctest.Route{Body: sharedNameACL})
	puts := putRecorder(srv, base, "read")
	c := newTestClient(t, srv.Server)

	if _, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "read",
		ACLMembers{Groups: []string{"ops"}}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	want := ACE{Actors: []string{}, Users: []string{"jenkins"}, Clients: []string{"jenkins"}, Groups: []string{"admins", "ops"}}
	if !reflect.DeepEqual(puts["read"], want) {
		t.Errorf("PUT read = %+v, want %+v", puts["read"], want)
	}
}

// Revoking the client leaves the user of the same name granted.
func TestACLs_RevokeClientKeepsSameNamedUser(t *testing.T) {
	const base = "/organizations/o/nodes/web01"
	srv := cinctest.New(t)
	srv.Handle("GET "+base+"/_acl", cinctest.Route{Body: sharedNameACL})
	puts := putRecorder(srv, base, "read")
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Revoke(context.Background(), ObjectACL(ACLNodes, "web01"), "all",
		ACLMembers{Clients: []string{"jenkins"}})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"read"}) {
		t.Errorf("changed = %v, want [read]", changed)
	}
	want := ACE{Actors: []string{}, Users: []string{"jenkins"}, Clients: []string{}, Groups: []string{"admins"}}
	if !reflect.DeepEqual(puts["read"], want) {
		t.Errorf("PUT read = %+v, want %+v", puts["read"], want)
	}
}

// A server that ignores detail=granular reports untyped actors, which Grant
// and Revoke cannot sort into users and clients. They refuse before writing
// anything rather than send a mixed ACE or miss a member.
func TestACLs_GrantRefusesUntypedActors(t *testing.T) {
	const flat = `{
	"create":{"actors":[],"groups":["admins"]},
	"read":{"actors":["alice"],"groups":["admins"]},
	"update":{"actors":[],"groups":["admins"]},
	"delete":{"actors":[],"groups":["admins"]},
	"grant":{"actors":[],"groups":["admins"]}
}`
	srv := cinctest.New(t) // only the GET: any write fails the test
	srv.Handle("GET /organizations/o/nodes/web01/_acl", cinctest.Route{Body: flat})
	c := newTestClient(t, srv.Server)

	changed, err := c.ACLs.Grant(context.Background(), ObjectACL(ACLNodes, "web01"), "all",
		ACLMembers{Groups: []string{"ops"}})
	if changed != nil || err == nil || !strings.Contains(err.Error(), "detail=granular") {
		t.Fatalf("Grant = %v, %v; want nil and an error naming detail=granular", changed, err)
	}
	if !strings.Contains(err.Error(), "read on nodes/web01") {
		t.Errorf("err = %v, want it to name the permission and target", err)
	}
}
