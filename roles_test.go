// roles_test.go
package cinc

import (
	"context"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestRoles_CRUD(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/roles/web",
		cinctest.Route{Body: `{"name":"web","run_list":["recipe[nginx]"],"default_attributes":{"port":80}}`})
	srv.Handle("POST /organizations/o/roles",
		cinctest.Route{Status: 201, Body: `{"uri":"http://x/roles/db"}`})
	srv.Handle("PUT /organizations/o/roles/web",
		cinctest.Route{Body: `{"name":"web"}`})
	srv.Handle("DELETE /organizations/o/roles/web", cinctest.Route{Body: `{}`})
	srv.Handle("GET /organizations/o/roles",
		cinctest.Route{Body: `{"web":"http://x/roles/web"}`})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	r, _, err := c.Roles.Get(ctx, "web")
	if err != nil || r.Name != "web" || len(r.RunList) != 1 {
		t.Fatalf("Get: %+v %v", r, err)
	}
	if _, _, err := c.Roles.Create(ctx, &Role{Name: "db"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := c.Roles.Update(ctx, &Role{Name: "web"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := c.Roles.Delete(ctx, "web"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if names, _, err := c.Roles.List(ctx); err != nil || names["web"] == "" {
		t.Fatalf("List: %+v %v", names, err)
	}
}

func TestRoles_Environments(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/roles/web/environments", cinctest.Route{
		Body: `["_default","production","qa"]`,
	})
	c := newTestClient(t, srv.Server)

	envs, _, err := c.Roles.Environments(context.Background(), "web")
	if err != nil {
		t.Fatalf("Environments: %v", err)
	}
	if len(envs) != 3 || envs[1] != "production" {
		t.Fatalf("Environments = %v", envs)
	}
}

func TestRoles_EnvironmentRunList(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/roles/web/environments/production", cinctest.Route{
		Body: `{"run_list":["recipe[nginx]","recipe[app]"]}`,
	})
	c := newTestClient(t, srv.Server)

	rl, _, err := c.Roles.EnvironmentRunList(context.Background(), "web", "production")
	if err != nil {
		t.Fatalf("EnvironmentRunList: %v", err)
	}
	if len(rl) != 2 || rl[1] != "recipe[app]" {
		t.Fatalf("EnvironmentRunList = %v", rl)
	}
}
