package cinc

import "context"

// Container is an ACL container — a grouping object that owns the
// "create" permission for new objects of one kind (nodes, clients, etc.).
// Chef returns containername and containerpath; they are almost always the
// same value, but the wire shape uses both keys.
type Container struct {
	Name string `json:"containername,omitempty"`
	Path string `json:"containerpath,omitempty"`
}

// ContainersService accesses the /containers endpoints.
type ContainersService struct{ client *Client }

// List returns the container name->URL index.
func (s *ContainersService) List(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET",
		s.client.orgPath("/containers"), nil)
}

// Get retrieves a single container by name.
func (s *ContainersService) Get(ctx context.Context, name string) (*Container, *Response, error) {
	cn, resp, err := do[Container](ctx, s.client, "GET",
		s.client.orgPath("/containers/"+esc(name)), nil)
	return ptrOrNil(cn, err), resp, err
}

// Create creates a new container. The Chef Server uses the same value for
// containername and containerpath, so a single name argument suffices.
func (s *ContainersService) Create(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "POST",
		s.client.orgPath("/containers"),
		Container{Name: name, Path: name})
	return resp, err
}

// Delete removes a container by name.
func (s *ContainersService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/containers/"+esc(name)), nil)
	return resp, err
}
