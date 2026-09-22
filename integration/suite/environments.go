package suite

import (
	"context"
	"errors"
	"net/http"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testEnvironmentLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	env := uniqueName(t, "env")
	node := uniqueName(t, "node")
	// The node is deleted before the environment it belongs to.
	cleanup(t, "environment "+env, func(ctx context.Context) error {
		_, err := c.Environments.Delete(ctx, env)
		return err
	})
	cleanup(t, "node "+node, func(ctx context.Context) error {
		_, err := c.Nodes.Delete(ctx, node)
		return err
	})

	if _, err := c.Environments.Create(ctx, &cinc.Environment{
		Name:              env,
		Description:       "staging",
		CookbookVersions:  map[string]string{"nginx": ">= 1.0.0"},
		DefaultAttributes: cinc.Attributes{"region": "us-west-2"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _, err := c.Environments.Get(ctx, env)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "staging" || got.CookbookVersions["nginx"] != ">= 1.0.0" {
		t.Fatalf("environment = %+v", got)
	}
	if got.DefaultAttributes["region"] != "us-west-2" {
		t.Fatalf("default_attributes = %v", got.DefaultAttributes)
	}

	list, _, err := c.Environments.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[env]; !ok {
		t.Fatalf("%s missing from environment list", env)
	}

	// A node placed in the environment is listed under it.
	if _, err := c.Nodes.Create(ctx, &cinc.Node{Name: node, Environment: env, RunList: []string{}}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	nodes, _, err := c.Environments.ListNodes(ctx, env)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if _, ok := nodes[node]; !ok {
		t.Fatalf("ListNodes(%s) = %v, want it to include %s", env, nodes, node)
	}

	got.Description = "staging (pinned)"
	got.CookbookVersions = map[string]string{"nginx": "= 1.2.0"}
	if _, _, err := c.Environments.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _, err := c.Environments.Get(ctx, env)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if again.Description != "staging (pinned)" || again.CookbookVersions["nginx"] != "= 1.2.0" {
		t.Fatalf("after update: environment = %+v", again)
	}

	if _, err := c.Nodes.Delete(ctx, node); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	if _, err := c.Environments.Delete(ctx, env); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := c.Environments.Get(ctx, env); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

// testEnvironmentDefaultReadOnly checks that the _default environment exists
// and cannot be modified: a Chef server answers a PUT to it with 405.
func testEnvironmentDefaultReadOnly(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	def, _, err := c.Environments.Get(ctx, "_default")
	if err != nil {
		t.Fatalf("Get _default: %v", err)
	}
	def.Description = "changed by " + t.Name()
	_, _, err = c.Environments.Update(ctx, def)
	wantStatus(t, err, http.StatusMethodNotAllowed)
}
