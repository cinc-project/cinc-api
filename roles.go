package cinc

import (
	"context"
	"encoding/json"
)

// Role is a Chef role object.
type Role struct {
	Name               string              `json:"name"`
	Description        string              `json:"description,omitempty"`
	RunList            []string            `json:"run_list"`
	DefaultAttributes  Attributes          `json:"default_attributes,omitempty"`
	OverrideAttributes Attributes          `json:"override_attributes,omitempty"`
	EnvRunLists        map[string][]string `json:"env_run_lists,omitempty"`
}

// MarshalJSON encodes the role with run_list always present as an array, for
// the same reason as Node.MarshalJSON.
func (r Role) MarshalJSON() ([]byte, error) {
	type alias Role
	a := alias(r)
	a.RunList = nonNil(a.RunList)
	return json.Marshal(a)
}

// RolesService accesses the /roles endpoints.
type RolesService struct{ client *Client }

func (s *RolesService) res() crud[Role] {
	return crud[Role]{client: s.client, path: "/roles"}
}

// Get retrieves a role by name.
func (s *RolesService) Get(ctx context.Context, name string) (*Role, *Response, error) {
	r, resp, err := s.res().get(ctx, name)
	return ptrOrNil(r, err), resp, err
}

// Create creates a new role.
func (s *RolesService) Create(ctx context.Context, r *Role) (*Role, *Response, error) {
	created, resp, err := s.res().create(ctx, r)
	return ptrOrNil(created, err), resp, err
}

// Update replaces an existing role.
func (s *RolesService) Update(ctx context.Context, r *Role) (*Role, *Response, error) {
	updated, resp, err := s.res().update(ctx, r.Name, r)
	return ptrOrNil(updated, err), resp, err
}

// Delete removes a role by name.
func (s *RolesService) Delete(ctx context.Context, name string) (*Response, error) {
	return s.res().remove(ctx, name)
}

// List returns the role name->URL index.
func (s *RolesService) List(ctx context.Context) (map[string]string, *Response, error) {
	return s.res().list(ctx)
}

// Environments lists the environments for which the role has an
// environment-specific run list (always including "_default").
func (s *RolesService) Environments(ctx context.Context, role string) ([]string, *Response, error) {
	return do[[]string](ctx, s.client, "GET",
		s.client.orgPath("/roles/"+esc(role)+"/environments"), nil)
}

// EnvironmentRunList returns the role's run list for one environment
// (env_run_lists[env], or the default run_list when env is "_default").
func (s *RolesService) EnvironmentRunList(ctx context.Context, role, env string) ([]string, *Response, error) {
	rl, resp, err := do[runListBody](ctx, s.client, "GET",
		s.client.orgPath("/roles/"+esc(role)+"/environments/"+esc(env)), nil)
	return rl.RunList, resp, err
}
