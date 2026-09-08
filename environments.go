package cinc

import (
	"context"
	"net/url"
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
func (s *EnvironmentsService) Create(ctx context.Context, e *Environment) (*Environment, *Response, error) {
	created, resp, err := s.res().create(ctx, e)
	return ptrOrNil(created, err), resp, err
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

// envCookbookQuery builds an environment cookbook path, appending the optional
// num_versions query parameter when non-empty.
func (s *EnvironmentsService) envPath(env, suffix, numVersions string) string {
	p := s.client.orgPath("/environments/" + esc(env) + suffix)
	if numVersions != "" {
		p += "?num_versions=" + url.QueryEscape(numVersions)
	}
	return p
}

// ListCookbooks returns the cookbooks (and versions) available to the
// environment, keyed by cookbook name. numVersions limits the versions per
// cookbook ("" for the server default, "all" for every version, or "n").
func (s *EnvironmentsService) ListCookbooks(ctx context.Context, env, numVersions string) (map[string]CookbookListEntry, *Response, error) {
	return do[map[string]CookbookListEntry](ctx, s.client, "GET",
		s.envPath(env, "/cookbooks", numVersions), nil)
}

// GetCookbook returns the versions of one cookbook available to the
// environment, filtered by the environment's version constraints.
func (s *EnvironmentsService) GetCookbook(ctx context.Context, env, name, numVersions string) (map[string]CookbookListEntry, *Response, error) {
	return do[map[string]CookbookListEntry](ctx, s.client, "GET",
		s.envPath(env, "/cookbooks/"+esc(name), numVersions), nil)
}

// CookbookVersions solves the given run list against the environment and
// returns the cookbook versions (including dependencies) required to satisfy
// it, keyed by cookbook name.
func (s *EnvironmentsService) CookbookVersions(ctx context.Context, env string, runList []string) (map[string]Cookbook, *Response, error) {
	return do[map[string]Cookbook](ctx, s.client, "POST",
		s.client.orgPath("/environments/"+esc(env)+"/cookbook_versions"),
		map[string][]string{"run_list": runList})
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
