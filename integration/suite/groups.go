package suite

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testGroupLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	client := newClient(t, c)
	inner := newGroup(t, c)
	group := newGroup(t, c)

	got, _, err := c.Groups.Get(ctx, group)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != group {
		t.Fatalf("group name = %q, want %q", got.Name, group)
	}

	list, _, err := c.Groups.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[group]; !ok {
		t.Fatalf("%s missing from group list", group)
	}

	updated, _, err := c.Groups.Update(ctx, &cinc.Group{Name: group, Clients: []string{client}, Groups: []string{inner}})
	if err != nil {
		t.Fatalf("Update (add members): %v", err)
	}
	// erchef answers the PUT by echoing its body, members nested under
	// "actors"; cinc-server-ng answers in the GET shape. Both must decode.
	if !slices.Contains(updated.Clients, client) || !slices.Contains(updated.Groups, inner) {
		t.Fatalf("Update returned %+v, want client %s and group %s", updated, client, inner)
	}
	again, _, err := c.Groups.Get(ctx, group)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if !slices.Contains(again.Clients, client) || !slices.Contains(again.Groups, inner) {
		t.Fatalf("after update: group = %+v, want client %s and group %s", again, client, inner)
	}

	if _, _, err := c.Groups.Update(ctx, &cinc.Group{Name: group}); err != nil {
		t.Fatalf("Update (remove members): %v", err)
	}
	emptied, _, err := c.Groups.Get(ctx, group)
	if err != nil {
		t.Fatalf("Get after emptying: %v", err)
	}
	if len(emptied.Clients) != 0 || len(emptied.Groups) != 0 {
		t.Fatalf("after emptying: group = %+v", emptied)
	}

	if _, err := c.Groups.Delete(ctx, group); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := c.Groups.Get(ctx, group); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

// testGroupMemberAddRemove changes each kind of member through AddMembers and
// RemoveMembers, and checks that a repeat is reported unchanged.
func testGroupMemberAddRemove(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	user, _ := newUser(t, c)
	client := newClient(t, c)
	inner := newGroup(t, c)
	group := newGroup(t, c)

	for kind, name := range map[cinc.MemberKind]string{
		cinc.MemberUser: user, cinc.MemberClient: client, cinc.MemberGroup: inner,
	} {
		got, err := c.Groups.AddMembers(ctx, group, kind, name)
		if err != nil {
			t.Fatalf("AddMembers(%s %s): %v", kind, name, err)
		}
		if !reflect.DeepEqual(got, &cinc.MemberChange{Changed: []string{name}}) {
			t.Errorf("AddMembers(%s %s) = %+v, want it changed", kind, name, got)
		}
		again, err := c.Groups.AddMembers(ctx, group, kind, name)
		if err != nil || !reflect.DeepEqual(again, &cinc.MemberChange{Unchanged: []string{name}}) {
			t.Errorf("repeat AddMembers(%s %s) = %+v, %v; want it unchanged", kind, name, again, err)
		}
	}
	g, _, err := c.Groups.Get(ctx, group)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !slices.Contains(g.Users, user) || !slices.Contains(g.Clients, client) || !slices.Contains(g.Groups, inner) {
		t.Fatalf("after adds: group = %+v", g)
	}

	got, err := c.Groups.RemoveMembers(ctx, group, cinc.MemberClient, client)
	if err != nil || !reflect.DeepEqual(got, &cinc.MemberChange{Changed: []string{client}}) {
		t.Fatalf("RemoveMembers(client %s) = %+v, %v; want it changed", client, got, err)
	}
	got, err = c.Groups.RemoveMembers(ctx, group, cinc.MemberClient, client)
	if err != nil || !reflect.DeepEqual(got, &cinc.MemberChange{Unchanged: []string{client}}) {
		t.Fatalf("repeat RemoveMembers(client %s) = %+v, %v; want it unchanged", client, got, err)
	}
	g, _, err = c.Groups.Get(ctx, group)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if slices.Contains(g.Clients, client) || !slices.Contains(g.Users, user) || !slices.Contains(g.Groups, inner) {
		t.Fatalf("after removing the client: group = %+v", g)
	}
}

// testGroupMemberAddDropsUnknown adds a real client and one that does not
// exist. The server accepts the PUT but silently leaves the unknown name out
// (erchef's oc_chef_group:update resolves each name to an authz id and skips
// the misses), which AddMembers must report as Dropped rather than Changed.
func testGroupMemberAddDropsUnknown(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	client := newClient(t, c)
	group := newGroup(t, c)
	ghost := uniqueName(t, "ghost")

	got, err := c.Groups.AddMembers(ctx, group, cinc.MemberClient, client, ghost)
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	want := &cinc.MemberChange{Changed: []string{client}, Dropped: []string{ghost}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AddMembers = %+v, want %+v", got, want)
	}
}

// newGroup creates a uniquely named group and registers its deletion.
func newGroup(t *testing.T, c *cinc.Client) string {
	t.Helper()
	name := uniqueName(t, "group")
	cleanup(t, "group "+name, func(ctx context.Context) error {
		_, err := c.Groups.Delete(ctx, name)
		return err
	})
	if _, err := c.Groups.Create(t.Context(), name); err != nil {
		t.Fatalf("create group: %v", err)
	}
	return name
}
