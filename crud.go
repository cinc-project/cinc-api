package cinc

import "context"

// crud is an internal helper implementing the Get/Create/Update/Delete/List
// pattern shared by most Chef resources. T is the resource struct type.
type crud[T any] struct {
	client *Client
	path   string // resource collection path, e.g. "/nodes"
}

func (r crud[T]) item(name string) string { return r.client.orgPath(r.path + "/" + esc(name)) }
func (r crud[T]) coll() string            { return r.client.orgPath(r.path) }

func (r crud[T]) get(ctx context.Context, name string) (T, *Response, error) {
	return do[T](ctx, r.client, "GET", r.item(name), nil)
}

func (r crud[T]) create(ctx context.Context, obj any) (T, *Response, error) {
	return do[T](ctx, r.client, "POST", r.coll(), obj)
}

func (r crud[T]) update(ctx context.Context, name string, obj any) (T, *Response, error) {
	return do[T](ctx, r.client, "PUT", r.item(name), obj)
}

func (r crud[T]) remove(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[T](ctx, r.client, "DELETE", r.item(name), nil)
	return resp, err
}

// list returns the Chef name->URL index for the collection.
func (r crud[T]) list(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, r.client, "GET", r.coll(), nil)
}
