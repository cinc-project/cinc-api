// environments_test.go
package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestEnvironments_CRUD(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/environments/prod",
		cinctest.Route{Body: `{"name":"prod","cookbook_versions":{"nginx":"= 1.2.0"}}`})
	srv.Handle("POST /organizations/o/environments",
		cinctest.Route{Status: 201, Body: `{"uri":"http://x/environments/dev"}`})
	srv.Handle("PUT /organizations/o/environments/prod",
		cinctest.Route{Body: `{"name":"prod"}`})
	srv.Handle("DELETE /organizations/o/environments/prod", cinctest.Route{Body: `{}`})
	srv.Handle("GET /organizations/o/environments",
		cinctest.Route{Body: `{"prod":"http://x/environments/prod"}`})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	e, _, err := c.Environments.Get(ctx, "prod")
	if err != nil || e.Name != "prod" || e.CookbookVersions["nginx"] != "= 1.2.0" {
		t.Fatalf("Get: %+v %v", e, err)
	}
	if _, _, err := c.Environments.Create(ctx, &Environment{Name: "dev"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := c.Environments.Update(ctx, &Environment{Name: "prod"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := c.Environments.Delete(ctx, "prod"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if names, _, err := c.Environments.List(ctx); err != nil || names["prod"] == "" {
		t.Fatalf("List: %+v %v", names, err)
	}
}

func TestEnvironments_ListCookbooks(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/environments/prod/cookbooks", cinctest.Route{
		Body: `{"apache2":{"url":"u","versions":[{"url":"u/5.1.0","version":"5.1.0"}]}}`,
		Assert: func(t *testing.T, r *http.Request, _ []byte) {
			if r.URL.Query().Get("num_versions") != "all" {
				t.Errorf("num_versions = %q", r.URL.Query().Get("num_versions"))
			}
		},
	})
	c := newTestClient(t, srv.Server)

	cbs, _, err := c.Environments.ListCookbooks(context.Background(), "prod", "all")
	if err != nil {
		t.Fatalf("ListCookbooks: %v", err)
	}
	if cbs["apache2"].Versions[0].Version != "5.1.0" {
		t.Fatalf("ListCookbooks = %+v", cbs)
	}
}

func TestEnvironments_GetCookbook(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/environments/prod/cookbooks/apache2", cinctest.Route{
		Body: `{"apache2":{"url":"u","versions":[{"url":"u/5.1.0","version":"5.1.0"}]}}`,
	})
	c := newTestClient(t, srv.Server)

	cb, _, err := c.Environments.GetCookbook(context.Background(), "prod", "apache2", "")
	if err != nil {
		t.Fatalf("GetCookbook: %v", err)
	}
	if cb["apache2"].Versions[0].Version != "5.1.0" {
		t.Fatalf("GetCookbook = %+v", cb)
	}
}

func TestEnvironments_CookbookVersions(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/environments/prod/cookbook_versions", cinctest.Route{
		Body: `{"apache2":{"cookbook_name":"apache2","name":"apache2-5.1.0","version":"5.1.0"}}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string][]string
			json.Unmarshal(body, &got)
			if len(got["run_list"]) != 2 || got["run_list"][0] != "recipe[apache2]" {
				t.Errorf("POST body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	cbs, _, err := c.Environments.CookbookVersions(context.Background(), "prod",
		[]string{"recipe[apache2]", "nginx"})
	if err != nil {
		t.Fatalf("CookbookVersions: %v", err)
	}
	if cbs["apache2"].Version != "5.1.0" {
		t.Fatalf("CookbookVersions = %+v", cbs)
	}
}

func TestEnvironments_ListNodes(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/environments/prod/nodes", cinctest.Route{
		Body: `{"web01":"https://x/nodes/web01"}`,
	})
	c := newTestClient(t, srv.Server)

	nodes, _, err := c.Environments.ListNodes(context.Background(), "prod")
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if nodes["web01"] == "" {
		t.Fatalf("ListNodes = %+v", nodes)
	}
}

func TestEnvironments_ListRecipes(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/environments/prod/recipes", cinctest.Route{
		Body: `["apache2","apache2::mod_ssl","nginx"]`,
	})
	c := newTestClient(t, srv.Server)

	recipes, _, err := c.Environments.ListRecipes(context.Background(), "prod")
	if err != nil {
		t.Fatalf("ListRecipes: %v", err)
	}
	if len(recipes) != 3 || recipes[1] != "apache2::mod_ssl" {
		t.Fatalf("ListRecipes = %v", recipes)
	}
}

func TestEnvironments_RoleRunList(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/environments/prod/roles/web", cinctest.Route{
		Body: `{"run_list":["recipe[apache2]","role[base]"]}`,
	})
	c := newTestClient(t, srv.Server)

	rl, _, err := c.Environments.RoleRunList(context.Background(), "prod", "web")
	if err != nil {
		t.Fatalf("RoleRunList: %v", err)
	}
	if len(rl) != 2 || rl[0] != "recipe[apache2]" {
		t.Fatalf("RoleRunList = %v", rl)
	}
}
