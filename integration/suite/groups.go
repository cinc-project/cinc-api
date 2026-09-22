package suite

import (
	"context"
	"errors"
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

	if _, _, err := c.Groups.Update(ctx, &cinc.Group{Name: group, Clients: []string{client}, Groups: []string{inner}}); err != nil {
		t.Fatalf("Update (add members): %v", err)
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
