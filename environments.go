package cinc

import (
	"context"
	"fmt"
	"strings"
)

// Environment is a Chef environment object.
type Environment struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	CookbookVersions   map[string]string `json:"cookbook_versions,omitempty"`
	DefaultAttributes  Attributes        `json:"default_attributes,omitempty"`
	OverrideAttributes Attributes        `json:"override_attributes,omitempty"`
}

// EnvironmentsService accesses the /environments endpoints.
type EnvironmentsService struct{ client *Client }

func (s *EnvironmentsService) res() crud[Environment] {
	return crud[Environment]{client: s.client, path: "/environments"}
}

// Get retrieves an environment by name.
func (s *EnvironmentsService) Get(ctx context.Context, name string) (*Environment, *Response, error) {
	e, resp, err := s.res().get(ctx, name)
	return ptrOrNil(e, err), resp, err
}

// Create creates a new environment.
//
// The Chef Server answers POST /environments with {"uri":...} rather than the
// created object, so there is nothing to return but the response and any
// error.
func (s *EnvironmentsService) Create(ctx context.Context, e *Environment) (*Response, error) {
	_, resp, err := s.res().create(ctx, e.Name, e)
	return resp, err
}

// Update replaces an existing environment.
func (s *EnvironmentsService) Update(ctx context.Context, e *Environment) (*Environment, *Response, error) {
	updated, resp, err := s.res().update(ctx, e.Name, e)
	return ptrOrNil(updated, err), resp, err
}

// Delete removes an environment by name.
func (s *EnvironmentsService) Delete(ctx context.Context, name string) (*Response, error) {
	return s.res().remove(ctx, name)
}

// List returns the environment name->URL index.
func (s *EnvironmentsService) List(ctx context.Context) (map[string]string, *Response, error) {
	return s.res().list(ctx)
}

// envPath is the path of an environment sub-endpoint.
func (s *EnvironmentsService) envPath(env, suffix string) string {
	return s.client.orgPath("/environments/" + esc(env) + suffix)
}

// ListCookbooks returns the cookbooks (and versions) available to the
// environment, keyed by cookbook name. numVersions limits the versions per
// cookbook: "" for the server default of one, "all" for every version, or
// "n" for the n newest; anything else is rejected before a request is sent.
// Versions are newest-first.
func (s *EnvironmentsService) ListCookbooks(ctx context.Context, env, numVersions string) (map[string]CookbookListEntry, *Response, error) {
	return getCookbookList(ctx, s.client, s.envPath(env, "/cookbooks"), numVersions, 1)
}

// GetCookbook returns the versions of one cookbook available to the
// environment, filtered by the environment's version constraints.
// numVersions is as for ListCookbooks, except that "" means every version
// (the server default here). Versions are newest-first.
func (s *EnvironmentsService) GetCookbook(ctx context.Context, env, name, numVersions string) (map[string]CookbookListEntry, *Response, error) {
	return getCookbookList(ctx, s.client, s.envPath(env, "/cookbooks/"+esc(name)), numVersions, allVersions)
}

// CookbookVersions solves the given run list against the environment and
// returns the cookbook versions (including dependencies) required to satisfy
// it, keyed by cookbook name.
//
// Each item names a recipe: "cookbook", "cookbook::recipe", either with an
// "@version" pin, or any of those wrapped in recipe[...], as a node's run
// list holds them. The server's depsolver reads only bare names (it splits
// each item on "@" and "::", so it would look recipe[x] up as a cookbook of
// that name), so recipe[...] is unwrapped before sending. It cannot expand a
// role: expand roles into their recipes first, as chef-client does. A role[...]
// item is an error and nothing is sent.
//
// When the run list cannot be solved the server answers 412; the
// *ErrorResponse's message names the missing cookbooks or failed
// constraints.
func (s *EnvironmentsService) CookbookVersions(ctx context.Context, env string, runList []string) (map[string]Cookbook, *Response, error) {
	items := make([]string, len(runList))
	for i, item := range runList {
		if strings.HasPrefix(item, "role[") {
			return nil, nil, fmt.Errorf("cinc: CookbookVersions: run list item %q is a role; expand roles into recipes first", item)
		}
		if inner, ok := strings.CutPrefix(item, "recipe["); ok {
			item = strings.TrimSuffix(inner, "]")
		}
		items[i] = item
	}
	return do[map[string]Cookbook](ctx, s.client, "POST",
		s.client.orgPath("/environments/"+esc(env)+"/cookbook_versions"),
		map[string][]string{"run_list": items})
}

// ListNodes returns the name->URL index of nodes in the environment.
func (s *EnvironmentsService) ListNodes(ctx context.Context, env string) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET",
		s.client.orgPath("/environments/"+esc(env)+"/nodes"), nil)
}

// ListRecipes returns the recipes available to the environment.
func (s *EnvironmentsService) ListRecipes(ctx context.Context, env string) ([]string, *Response, error) {
	return do[[]string](ctx, s.client, "GET",
		s.client.orgPath("/environments/"+esc(env)+"/recipes"), nil)
}

// RoleRunList returns the role's run list as scoped to the environment: the
// role's env_run_lists[env], or its default run_list for the _default
// environment.
func (s *EnvironmentsService) RoleRunList(ctx context.Context, env, role string) ([]string, *Response, error) {
	rl, resp, err := do[runListBody](ctx, s.client, "GET",
		s.client.orgPath("/environments/"+esc(env)+"/roles/"+esc(role)), nil)
	return rl.RunList, resp, err
}

// runListBody decodes the {"run_list":[...]} responses returned by the
// environment- and role-scoped run-list endpoints.
type runListBody struct {
	RunList []string `json:"run_list"`
}
