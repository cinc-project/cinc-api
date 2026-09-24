package suite

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testObjectACLs grants a client read on one object of each kind, checks the
// grant is visible, then revokes it. Cookbooks, cookbook artifacts, policies
// and policy groups are covered with the cookbook and policy cases, which
// already create those objects.
func testObjectACLs(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	kinds := []struct {
		segment string
		create  func(t *testing.T) string
	}{
		{"nodes", func(t *testing.T) string {
			name := uniqueName(t, "node")
			cleanup(t, "node "+name, func(ctx context.Context) error { _, err := c.Nodes.Delete(ctx, name); return err })
			if _, err := c.Nodes.Create(ctx, &cinc.Node{Name: name, RunList: []string{}}); err != nil {
				t.Fatalf("create node: %v", err)
			}
			return name
		}},
		{"roles", func(t *testing.T) string {
			name := uniqueName(t, "role")
			cleanup(t, "role "+name, func(ctx context.Context) error { _, err := c.Roles.Delete(ctx, name); return err })
			if _, err := c.Roles.Create(ctx, &cinc.Role{Name: name, RunList: []string{}}); err != nil {
				t.Fatalf("create role: %v", err)
			}
			return name
		}},
		{"environments", func(t *testing.T) string {
			name := uniqueName(t, "env")
			cleanup(t, "environment "+name, func(ctx context.Context) error { _, err := c.Environments.Delete(ctx, name); return err })
			if _, err := c.Environments.Create(ctx, &cinc.Environment{Name: name}); err != nil {
				t.Fatalf("create environment: %v", err)
			}
			return name
		}},
		{"clients", func(t *testing.T) string { return newClient(t, c) }},
		{"data", func(t *testing.T) string { return newDataBag(t, c) }},
		{"groups", func(t *testing.T) string { return newGroup(t, c) }},
		{"containers", func(t *testing.T) string { return newContainer(t, c) }},
	}

	grantee := newClient(t, c)
	for _, kind := range kinds {
		t.Run(kind.segment, func(t *testing.T) {
			name := kind.create(t)
			checkGrantRevoke(t, grantee,
				func() (*cinc.ACL, error) { acl, _, err := c.ACLs.Get(ctx, kind.segment, name); return acl, err },
				func(ace *cinc.ACE) error { return c.ACLs.SetPermission(ctx, kind.segment, name, "read", ace) })
		})
	}
}

func testOrgACL(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	checkGrantRevoke(t, newClient(t, c),
		func() (*cinc.ACL, error) { acl, _, err := c.ACLs.GetOrg(ctx); return acl, err },
		func(ace *cinc.ACE) error { return c.ACLs.SetOrgPermission(ctx, "read", ace) })
}

// testUserACL reads the admin user's ACL. It does not change it: the admin
// is the identity every other case authenticates as.
func testUserACL(t *testing.T, tgt Target, c *cinc.Client) {
	acl, _, err := c.ACLs.GetUser(t.Context(), tgt.Admin)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if len(acl.Grant.Actors)+len(acl.Grant.Groups) == 0 {
		t.Fatalf("user %s's grant ACE is empty: %+v", tgt.Admin, acl)
	}
}

// checkGrantRevoke adds actor to the read ACE through set, checks get shows
// it, removes it again and checks it is gone. Everything else in the ACE is
// written back unchanged.
func checkGrantRevoke(t *testing.T, actor string, get func() (*cinc.ACL, error), set func(*cinc.ACE) error) {
	t.Helper()
	acl, err := get()
	if err != nil {
		t.Fatalf("get ACL: %v", err)
	}
	read := acl.Read
	if slices.Contains(read.Actors, actor) {
		t.Fatalf("%s already has read before the grant: %+v", actor, read)
	}

	read.AddMembers([]string{actor}, nil)
	if err := set(&read); err != nil {
		t.Fatalf("grant read: %v", err)
	}
	if acl, err = get(); err != nil {
		t.Fatalf("get ACL after grant: %v", err)
	}
	if !slices.Contains(acl.Read.Actors, actor) {
		t.Fatalf("after grant: read = %+v, want actor %s", acl.Read, actor)
	}

	read = acl.Read
	read.RemoveMembers([]string{actor}, nil)
	if err := set(&read); err != nil {
		t.Fatalf("revoke read: %v", err)
	}
	if acl, err = get(); err != nil {
		t.Fatalf("get ACL after revoke: %v", err)
	}
	if slices.Contains(acl.Read.Actors, actor) {
		t.Fatalf("after revoke: read = %+v still has %s", acl.Read, actor)
	}
}

// testCookbookObjectACLs does the grant/revoke check on a cookbook, a cookbook
// artifact, a policy and a policy group.
func testCookbookObjectACLs(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	cookbook := uniqueName(t, "cookbook")
	uploadCookbook(t, c, cookbook, "1.0.0", nil)
	artifact, _ := uploadArtifact(t, c)
	group := uniqueName(t, "group")
	p := pushPolicy(t, c, group)

	grantee := newClient(t, c)
	for _, obj := range []struct{ segment, name string }{
		{"cookbooks", cookbook},
		{"cookbook_artifacts", artifact},
		{"policies", p.name},
		{"policy_groups", group},
	} {
		t.Run(obj.segment, func(t *testing.T) {
			checkGrantRevoke(t, grantee,
				func() (*cinc.ACL, error) { acl, _, err := c.ACLs.Get(ctx, obj.segment, obj.name); return acl, err },
				func(ace *cinc.ACE) error { return c.ACLs.SetPermission(ctx, obj.segment, obj.name, "read", ace) })
		})
	}
}

// testGrantRevoke round-trips ACLs.Grant and ACLs.Revoke on a data bag (every
// permission, a client and a group at once), on the organization (one
// permission other than the read that acls/org rewrites, so the two cases never
// race on one ACE) and on a global user.
func testGrantRevoke(t *testing.T, _ Target, c *cinc.Client) {
	client, group := newClient(t, c), newGroup(t, c)
	owner, _ := newUser(t, c)
	grantee, _ := newUser(t, c)
	for _, tc := range []struct {
		target         cinc.ACLTarget
		perm           string
		actors, groups []string
		want           []string
	}{
		{cinc.ObjectACL(cinc.ACLDataBags, newDataBag(t, c)), "all", []string{client}, []string{group}, cinc.ACLPerms},
		{cinc.OrgACL(), "delete", []string{client}, nil, []string{"delete"}},
		{cinc.UserACL(owner), "read", []string{grantee}, nil, []string{"read"}},
	} {
		t.Run(tc.target.ObjectType(), func(t *testing.T) {
			ctx := t.Context()
			before, _, err := c.ACLs.GetTarget(ctx, tc.target)
			if err != nil {
				t.Fatalf("GetTarget: %v", err)
			}
			changed, err := c.ACLs.Grant(ctx, tc.target, tc.perm, tc.actors, tc.groups)
			if err != nil {
				t.Fatalf("Grant: %v", err)
			}
			if !slices.Equal(changed, tc.want) {
				t.Fatalf("Grant changed %v, want %v", changed, tc.want)
			}
			after, _, err := c.ACLs.GetTarget(ctx, tc.target)
			if err != nil {
				t.Fatalf("GetTarget after grant: %v", err)
			}
			for _, perm := range tc.want {
				ace, _ := after.ACEFor(perm)
				for _, a := range tc.actors {
					if !slices.Contains(ace.Actors, a) {
						t.Errorf("after grant: %s = %+v, want actor %s", perm, ace, a)
					}
				}
				for _, g := range tc.groups {
					if !slices.Contains(ace.Groups, g) {
						t.Errorf("after grant: %s = %+v, want group %s", perm, ace, g)
					}
				}
			}

			// Granting again changes nothing and sends nothing.
			if changed, err := c.ACLs.Grant(ctx, tc.target, tc.perm, tc.actors, tc.groups); err != nil || changed != nil {
				t.Fatalf("second Grant = %v, %v; want no change", changed, err)
			}

			changed, err = c.ACLs.Revoke(ctx, tc.target, tc.perm, tc.actors, tc.groups)
			if err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if !slices.Equal(changed, tc.want) {
				t.Fatalf("Revoke changed %v, want %v", changed, tc.want)
			}
			restored, _, err := c.ACLs.GetTarget(ctx, tc.target)
			if err != nil {
				t.Fatalf("GetTarget after revoke: %v", err)
			}
			for _, perm := range tc.want {
				was, _ := before.ACEFor(perm)
				now, _ := restored.ACEFor(perm)
				if !sameMembers(was.Actors, now.Actors) || !sameMembers(was.Groups, now.Groups) {
					t.Errorf("after revoke: %s = %+v, want it back to %+v", perm, now, was)
				}
			}
		})
	}
}

// sameMembers reports whether a and b hold the same names, in any order.
func sameMembers(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// testGrantRejectsUnknownMembers: erchef resolves every ACE member to an authz
// id before writing, and refuses a name that resolves to nothing with a 400
// (oc_chef_wm_acl_permission: bad_actor, invalid group), as it does a client
// in a global user's ACL, which holds users only. The refused write is the
// first one, so nothing is reported as changed.
func testGrantRejectsUnknownMembers(t *testing.T, _ Target, c *cinc.Client) {
	bag := cinc.ObjectACL(cinc.ACLDataBags, newDataBag(t, c))
	user, _ := newUser(t, c)
	for _, tc := range []struct {
		name           string
		target         cinc.ACLTarget
		actors, groups []string
	}{
		{"actor", bag, []string{uniqueName(t, "ghost")}, nil},
		{"group", bag, nil, []string{uniqueName(t, "ghost")}},
		{"client-on-user", cinc.UserACL(user), []string{newClient(t, c)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, err := c.ACLs.Grant(t.Context(), tc.target, "all", tc.actors, tc.groups)
			if changed != nil {
				t.Errorf("changed = %v, want nil", changed)
			}
			var ce *cinc.ACLChangeError
			if !errors.As(err, &ce) || ce.Perm != "create" {
				t.Fatalf("Grant = %v, want an *ACLChangeError on create", err)
			}
			if !errors.Is(err, cinc.ErrBadRequest) {
				t.Fatalf("Grant = %v, want ErrBadRequest", err)
			}
		})
	}
}

// testRevokeKeepsAdminsOnGrant: erchef refuses to take the admins group off an
// object's grant ACE for anyone but the superuser, with a 403 rather than a
// 400 (oc_chef_authz_acl_constraints: attempted_admin_group_removal_grant_ace).
func testRevokeKeepsAdminsOnGrant(t *testing.T, _ Target, c *cinc.Client) {
	bag := cinc.ObjectACL(cinc.ACLDataBags, newDataBag(t, c))
	changed, err := c.ACLs.Revoke(t.Context(), bag, "grant", nil, []string{"admins"})
	if changed != nil {
		t.Errorf("changed = %v, want nil", changed)
	}
	wantStatus(t, err, http.StatusForbidden)
}
