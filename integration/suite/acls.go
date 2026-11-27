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
			checkGrantRevoke(t, c, cinc.ObjectACL(kind.segment, name), grantee)
		})
	}
}

func testOrgACL(t *testing.T, _ Target, c *cinc.Client) {
	checkGrantRevoke(t, c, cinc.OrgACL(), newClient(t, c))
}

// testUserACL reads the admin user's ACL. It does not change it: the admin
// is the identity every other case authenticates as.
func testUserACL(t *testing.T, tgt Target, c *cinc.Client) {
	acl, _, err := c.ACLs.Get(t.Context(), cinc.UserACL(tgt.Admin))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(acl.Grant.Users)+len(acl.Grant.Groups) == 0 {
		t.Fatalf("user %s's grant ACE is empty: %+v", tgt.Admin, acl)
	}
}

// checkGrantRevoke adds client to target's read ACE through SetPermission,
// checks Get shows it, removes it again and checks it is gone. Everything else
// in the ACE is written back unchanged.
func checkGrantRevoke(t *testing.T, c *cinc.Client, target cinc.ACLTarget, client string) {
	t.Helper()
	ctx := t.Context()
	acl, _, err := c.ACLs.Get(ctx, target)
	if err != nil {
		t.Fatalf("get ACL: %v", err)
	}
	read := acl.Read
	if len(read.Actors) > 0 {
		t.Fatalf("read = %+v: Get should report users and clients apart, with actors empty", read)
	}
	if slices.Contains(read.Clients, client) {
		t.Fatalf("%s already has read before the grant: %+v", client, read)
	}

	read.AddMembers(cinc.ACLMembers{Clients: []string{client}})
	if _, err := c.ACLs.SetPermission(ctx, target, "read", &read); err != nil {
		t.Fatalf("grant read: %v", err)
	}
	if acl, _, err = c.ACLs.Get(ctx, target); err != nil {
		t.Fatalf("get ACL after grant: %v", err)
	}
	if !slices.Contains(acl.Read.Clients, client) {
		t.Fatalf("after grant: read = %+v, want client %s", acl.Read, client)
	}

	read = acl.Read
	read.RemoveMembers(cinc.ACLMembers{Clients: []string{client}})
	if _, err := c.ACLs.SetPermission(ctx, target, "read", &read); err != nil {
		t.Fatalf("revoke read: %v", err)
	}
	if acl, _, err = c.ACLs.Get(ctx, target); err != nil {
		t.Fatalf("get ACL after revoke: %v", err)
	}
	if slices.Contains(acl.Read.Clients, client) {
		t.Fatalf("after revoke: read = %+v still has %s", acl.Read, client)
	}
}

// testCookbookObjectACLs does the grant/revoke check on a cookbook, a cookbook
// artifact, a policy and a policy group.
func testCookbookObjectACLs(t *testing.T, _ Target, c *cinc.Client) {
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
			checkGrantRevoke(t, c, cinc.ObjectACL(obj.segment, obj.name), grantee)
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
		target  cinc.ACLTarget
		perm    string
		members cinc.ACLMembers
		want    []string
	}{
		{cinc.ObjectACL(cinc.ACLDataBags, newDataBag(t, c)), "all",
			cinc.ACLMembers{Clients: []string{client}, Groups: []string{group}}, cinc.ACLPerms()},
		{cinc.OrgACL(), "delete", cinc.ACLMembers{Clients: []string{client}}, []string{"delete"}},
		{cinc.UserACL(owner), "read", cinc.ACLMembers{Users: []string{grantee}}, []string{"read"}},
	} {
		t.Run(tc.target.ObjectType(), func(t *testing.T) {
			ctx := t.Context()
			before, _, err := c.ACLs.Get(ctx, tc.target)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			changed, err := c.ACLs.Grant(ctx, tc.target, tc.perm, tc.members)
			if err != nil {
				t.Fatalf("Grant: %v", err)
			}
			if !slices.Equal(changed, tc.want) {
				t.Fatalf("Grant changed %v, want %v", changed, tc.want)
			}
			after, _, err := c.ACLs.Get(ctx, tc.target)
			if err != nil {
				t.Fatalf("Get after grant: %v", err)
			}
			for _, perm := range tc.want {
				ace, _ := after.ACEFor(perm)
				if !holdsAll(ace, tc.members) {
					t.Errorf("after grant: %s = %+v, want it to hold %+v", perm, ace, tc.members)
				}
			}

			// Granting again changes nothing and sends nothing.
			if changed, err := c.ACLs.Grant(ctx, tc.target, tc.perm, tc.members); err != nil || changed != nil {
				t.Fatalf("second Grant = %v, %v; want no change", changed, err)
			}

			changed, err = c.ACLs.Revoke(ctx, tc.target, tc.perm, tc.members)
			if err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if !slices.Equal(changed, tc.want) {
				t.Fatalf("Revoke changed %v, want %v", changed, tc.want)
			}
			restored, _, err := c.ACLs.Get(ctx, tc.target)
			if err != nil {
				t.Fatalf("Get after revoke: %v", err)
			}
			for _, perm := range tc.want {
				was, _ := before.ACEFor(perm)
				now, _ := restored.ACEFor(perm)
				if !sameACE(was, now) {
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

// sameACE reports whether a and b hold the same members of every kind.
func sameACE(a, b *cinc.ACE) bool {
	return sameMembers(a.Actors, b.Actors) && sameMembers(a.Users, b.Users) &&
		sameMembers(a.Clients, b.Clients) && sameMembers(a.Groups, b.Groups)
}

// holdsAll reports whether ace holds every member m names, each as its kind.
func holdsAll(ace *cinc.ACE, m cinc.ACLMembers) bool {
	contains := func(list, names []string) bool {
		return !slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(list, n) })
	}
	return contains(ace.Users, m.Users) && contains(ace.Clients, m.Clients) && contains(ace.Groups, m.Groups)
}

// testGrantRejectsUnknownMembers: erchef resolves every ACE member to an authz
// id before writing, and refuses a name that resolves to nothing with a 400
// (oc_chef_wm_acl_permission: invalid user, client or group), as it does a
// client in a global user's ACL, which holds users only. The refused write is the
// first one, so nothing is reported as changed.
func testGrantRejectsUnknownMembers(t *testing.T, _ Target, c *cinc.Client) {
	bag := cinc.ObjectACL(cinc.ACLDataBags, newDataBag(t, c))
	user, _ := newUser(t, c)
	for _, tc := range []struct {
		name    string
		target  cinc.ACLTarget
		members cinc.ACLMembers
	}{
		{"user", bag, cinc.ACLMembers{Users: []string{uniqueName(t, "ghost")}}},
		{"client", bag, cinc.ACLMembers{Clients: []string{uniqueName(t, "ghost")}}},
		{"group", bag, cinc.ACLMembers{Groups: []string{uniqueName(t, "ghost")}}},
		{"client-on-user", cinc.UserACL(user), cinc.ACLMembers{Clients: []string{newClient(t, c)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, err := c.ACLs.Grant(t.Context(), tc.target, "all", tc.members)
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
	changed, err := c.ACLs.Revoke(t.Context(), bag, "grant", cinc.ACLMembers{Groups: []string{"admins"}})
	if changed != nil {
		t.Errorf("changed = %v, want nil", changed)
	}
	wantStatus(t, err, http.StatusForbidden)
	if !errors.Is(err, cinc.ErrAdminsRequiredOnGrant) {
		t.Errorf("errors.Is(err, ErrAdminsRequiredOnGrant) = false: %v", err)
	}
}

// testSameNamedUserAndClient grants and revokes a user and a client that share
// a name, the user a member of the org, independently. In the flat form erchef
// cannot tell them apart and refuses any ACE naming them with a 422
// (oc_chef_authz_acl:fetch_actors, ambiguous_actor; pedant account_acl_spec
// "when a client exists with the same name as a user"), so before Grant wrote
// users and clients apart, once both held a permission no later change to it
// could be written at all.
func testSameNamedUserAndClient(t *testing.T, tgt Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "twin")
	cleanup(t, "user "+name, func(ctx context.Context) error { _, err := c.Users.Delete(ctx, name); return err })
	res, _, err := c.Users.Create(ctx, &cinc.User{
		UserName: name, DisplayName: name, Email: name + "@example.com",
		FirstName: "Test", LastName: "User", Password: userPassword(name), CreateKey: true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// Membership through an accepted invitation: a direct association needs
	// the superuser on erchef.
	id := invite(t, c, name)
	if _, err := clientAs(t, tgt, name, res.ChefKey.PrivateKey).Associations.RespondInvite(ctx, name, id, true); err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	cleanup(t, "membership "+name, func(ctx context.Context) error { _, _, err := c.Associations.RemoveMember(ctx, name); return err })
	cleanup(t, "client "+name, func(ctx context.Context) error { _, err := c.Clients.Delete(ctx, name); return err })
	if _, _, err := c.Clients.Create(ctx, &cinc.APIClient{Name: name}); err != nil {
		t.Fatalf("create client: %v", err)
	}

	bag := cinc.ObjectACL(cinc.ACLDataBags, newDataBag(t, c))
	read := func() *cinc.ACE {
		t.Helper()
		acl, _, err := c.ACLs.Get(ctx, bag)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return &acl.Read
	}
	grant := func(m cinc.ACLMembers) {
		t.Helper()
		if changed, err := c.ACLs.Grant(ctx, bag, "read", m); err != nil || len(changed) != 1 {
			t.Fatalf("Grant(%+v) = %v, %v; want read changed", m, changed, err)
		}
	}
	revoke := func(m cinc.ACLMembers) {
		t.Helper()
		if changed, err := c.ACLs.Revoke(ctx, bag, "read", m); err != nil || len(changed) != 1 {
			t.Fatalf("Revoke(%+v) = %v, %v; want read changed", m, changed, err)
		}
	}

	grant(cinc.ACLMembers{Users: []string{name}})
	if ace := read(); !slices.Contains(ace.Users, name) || slices.Contains(ace.Clients, name) {
		t.Fatalf("after granting the user: read = %+v, want %s as a user only", ace, name)
	}
	grant(cinc.ACLMembers{Clients: []string{name}})
	if ace := read(); !slices.Contains(ace.Users, name) || !slices.Contains(ace.Clients, name) {
		t.Fatalf("after granting the client: read = %+v, want %s as both", ace, name)
	}

	// With both on read, a change to another member still writes.
	group := newGroup(t, c)
	grant(cinc.ACLMembers{Groups: []string{group}})
	revoke(cinc.ACLMembers{Groups: []string{group}})

	revoke(cinc.ACLMembers{Clients: []string{name}})
	if ace := read(); !slices.Contains(ace.Users, name) || slices.Contains(ace.Clients, name) {
		t.Fatalf("after revoking the client: read = %+v, want %s as a user only", ace, name)
	}
	revoke(cinc.ACLMembers{Users: []string{name}})
	if ace := read(); slices.Contains(ace.Users, name) || slices.Contains(ace.Clients, name) {
		t.Fatalf("after revoking the user: read = %+v still has %s", ace, name)
	}
}
