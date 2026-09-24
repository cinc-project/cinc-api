package suite

import (
	"context"
	"errors"
	"slices"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testNodeLifecycle(t *testing.T, _ Target, c *cinc.Client) {
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

func testNodeNotFound(t *testing.T, _ Target, c *cinc.Client) {
	_, _, err := c.Nodes.Get(t.Context(), uniqueName(t, "missing"))
	if !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get missing node: err = %v, want ErrNotFound", err)
	}
}

func testNodeModify(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "node")
	cleanup(t, "node "+name, func(ctx context.Context) error {
		_, err := c.Nodes.Delete(ctx, name)
		return err
	})
	if _, err := c.Nodes.Create(ctx, &cinc.Node{
		Name: name, Environment: "_default", RunList: []string{"recipe[base]"},
		Normal: cinc.Attributes{"tags": []string{"prod"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A change that leaves the node as it is sends nothing.
	if _, sent, err := c.Nodes.Modify(ctx, name, func(n *cinc.Node) error {
		n.AddTags("prod")
		return nil
	}); err != nil || sent {
		t.Fatalf("no-op Modify: sent=%v err=%v, want no PUT", sent, err)
	}

	got, sent, err := c.Nodes.Modify(ctx, name, func(n *cinc.Node) error {
		n.AddTags("web")
		n.Normal["owner"] = "team-a"
		return nil
	})
	if err != nil || !sent {
		t.Fatalf("Modify: sent=%v err=%v", sent, err)
	}
	// The returned node is the server's PUT response.
	if got.Name != name || !slices.Equal(got.Tags(), []string{"prod", "web"}) {
		t.Fatalf("Modify returned %+v, want tags [prod web]", got)
	}
	again, _, err := c.Nodes.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !slices.Equal(again.Tags(), []string{"prod", "web"}) || again.Normal["owner"] != "team-a" ||
		!slices.Equal(again.RunList, []string{"recipe[base]"}) {
		t.Fatalf("stored node = %+v, want tags [prod web], owner team-a, run list kept", again)
	}

	// A rename is refused before anything is sent.
	if _, sent, err := c.Nodes.Modify(ctx, name, func(n *cinc.Node) error {
		n.Name = name + "-renamed"
		return nil
	}); err == nil || sent {
		t.Fatalf("renaming Modify: sent=%v err=%v, want an error and no PUT", sent, err)
	}
	if _, _, err := c.Nodes.Get(ctx, name); err != nil {
		t.Fatalf("Get after refused rename: %v", err)
	}
}

// testNodeRunListEditNormalized starts from a run list stored as a knife user
// would type it (a bare "nginx"), which erchef normalizes on save and
// cinc-server-ng keeps verbatim. Either way, Add/RemoveRunListItems compare
// normalized forms and write the normalized list, so the stored result is the
// same on both servers.
func testNodeRunListEditNormalized(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "node")
	cleanup(t, "node "+name, func(ctx context.Context) error {
		_, err := c.Nodes.Delete(ctx, name)
		return err
	})
	if _, err := c.Nodes.Create(ctx, &cinc.Node{
		Name: name, RunList: []string{"nginx", "recipe[base]"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, _, err := c.Nodes.Modify(ctx, name, func(n *cinc.Node) error {
		n.RemoveRunListItems("recipe[nginx]")
		n.AddRunListItems("base", "apache2::server", "role[web]")
		return nil
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}

	got, _, err := c.Nodes.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []string{"recipe[base]", "recipe[apache2::server]", "role[web]"}
	if !slices.Equal(got.RunList, want) {
		t.Fatalf("run_list = %q, want %q", got.RunList, want)
	}
}
