package suite

import (
	"context"
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
