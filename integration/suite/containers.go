package suite

import (
	"context"
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testContainerLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := newContainer(t, c)

	got, _, err := c.Containers.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != name || got.Path != name {
		t.Fatalf("container = %+v, want name and path %q", got, name)
	}

	list, _, err := c.Containers.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[name]; !ok {
		t.Fatalf("%s missing from container list", name)
	}
	// The built-in containers every org has are listed too.
	for _, builtin := range []string{"nodes", "clients", "roles"} {
		if _, ok := list[builtin]; !ok {
			t.Errorf("built-in container %q missing from list", builtin)
		}
	}

	if _, err := c.Containers.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := c.Containers.Get(ctx, name); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

// newContainer creates a uniquely named container and registers its deletion.
func newContainer(t *testing.T, c *cinc.Client) string {
	t.Helper()
	name := uniqueName(t, "container")
	cleanup(t, "container "+name, func(ctx context.Context) error {
		_, err := c.Containers.Delete(ctx, name)
		return err
	})
	if _, err := c.Containers.Create(t.Context(), name); err != nil {
		t.Fatalf("create container: %v", err)
	}
	return name
}
