package suite

import (
	"context"
	"errors"
	"slices"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testRoleLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	role := uniqueName(t, "role")
	env := uniqueName(t, "env")
	// Cleanups run last-registered first: the role goes before the
	// environment its env_run_lists names.
	cleanup(t, "environment "+env, func(ctx context.Context) error {
		_, err := c.Environments.Delete(ctx, env)
		return err
	})
	cleanup(t, "role "+role, func(ctx context.Context) error {
		_, err := c.Roles.Delete(ctx, role)
		return err
	})

	if _, err := c.Environments.Create(ctx, &cinc.Environment{Name: env}); err != nil {
		t.Fatalf("create environment: %v", err)
	}
	if _, err := c.Roles.Create(ctx, &cinc.Role{
		Name:              role,
		Description:       "web tier",
		RunList:           []string{"recipe[base]"},
		DefaultAttributes: cinc.Attributes{"tier": "web"},
		EnvRunLists:       map[string][]string{env: {"recipe[base]", "recipe[nginx]"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _, err := c.Roles.Get(ctx, role)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "web tier" || !slices.Equal(got.RunList, []string{"recipe[base]"}) {
		t.Fatalf("role = %+v", got)
	}
	if got.DefaultAttributes["tier"] != "web" {
		t.Fatalf("default_attributes = %v", got.DefaultAttributes)
	}

	envs, _, err := c.Roles.Environments(ctx, role)
	if err != nil {
		t.Fatalf("Environments: %v", err)
	}
	if !slices.Contains(envs, env) {
		t.Fatalf("Environments(%s) = %v, want it to include %s", role, envs, env)
	}
	runList, _, err := c.Roles.EnvironmentRunList(ctx, role, env)
	if err != nil {
		t.Fatalf("EnvironmentRunList: %v", err)
	}
	if !slices.Equal(runList, []string{"recipe[base]", "recipe[nginx]"}) {
		t.Fatalf("EnvironmentRunList = %v", runList)
	}
	// The same run list is reachable from the environment's side.
	fromEnv, _, err := c.Environments.RoleRunList(ctx, env, role)
	if err != nil {
		t.Fatalf("Environments.RoleRunList: %v", err)
	}
	if !slices.Equal(fromEnv, runList) {
		t.Fatalf("Environments.RoleRunList = %v, want %v", fromEnv, runList)
	}

	list, _, err := c.Roles.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[role]; !ok {
		t.Fatalf("%s missing from role list", role)
	}

	got.Description = "web and api tier"
	got.RunList = append(got.RunList, "recipe[api]")
	if _, _, err := c.Roles.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _, err := c.Roles.Get(ctx, role)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if again.Description != "web and api tier" || !slices.Equal(again.RunList, []string{"recipe[base]", "recipe[api]"}) {
		t.Fatalf("after update: role = %+v", again)
	}

	if _, err := c.Roles.Delete(ctx, role); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := c.Roles.Get(ctx, role); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

// testRoleRunListEditNormalized is the role counterpart of
// testNodeRunListEditNormalized.
func testRoleRunListEditNormalized(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "role")
	cleanup(t, "role "+name, func(ctx context.Context) error {
		_, err := c.Roles.Delete(ctx, name)
		return err
	})
	if _, err := c.Roles.Create(ctx, &cinc.Role{
		Name: name, RunList: []string{"nginx", "recipe[base]"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	r, _, err := c.Roles.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	r.RemoveRunListItems("recipe[nginx]")
	r.AddRunListItems("base", "apache2")
	if _, _, err := c.Roles.Update(ctx, r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _, err := c.Roles.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	want := []string{"recipe[base]", "recipe[apache2]"}
	if !slices.Equal(got.RunList, want) {
		t.Fatalf("run_list = %q, want %q", got.RunList, want)
	}
}
