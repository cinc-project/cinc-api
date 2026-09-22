package suite

import (
	"context"
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testNodeLifecycle(t *testing.T, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "node")
	cleanup(t, "node "+name, func(ctx context.Context) error {
		_, err := c.Nodes.Delete(ctx, name)
		return err
	})

	// A Chef server answers POST /nodes with {"uri": "..."} rather than the
	// full node object, so the returned value is intentionally not asserted
	// on here — creation is verified through the subsequent Get.
	if _, err := c.Nodes.Create(ctx, &cinc.Node{
		Name: name, Environment: "_default", RunList: []string{"recipe[nginx]"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _, err := c.Nodes.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.RunList) != 1 || got.RunList[0] != "recipe[nginx]" {
		t.Fatalf("run_list = %v, want [recipe[nginx]]", got.RunList)
	}

	got.RunList = append(got.RunList, "recipe[base]")
	if _, _, err := c.Nodes.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if again, _, err := c.Nodes.Get(ctx, name); err != nil || len(again.RunList) != 2 {
		t.Fatalf("after update: node=%+v err=%v", again, err)
	}

	list, _, err := c.Nodes.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[name]; !ok {
		t.Fatalf("%s missing from node list", name)
	}

	if _, err := c.Nodes.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// The server returns 404, which the client maps to ErrNotFound.
	if _, _, err := c.Nodes.Get(ctx, name); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

func testNodeNotFound(t *testing.T, c *cinc.Client) {
	_, _, err := c.Nodes.Get(t.Context(), uniqueName(t, "missing"))
	if !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get missing node: err = %v, want ErrNotFound", err)
	}
}
