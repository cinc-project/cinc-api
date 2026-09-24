package cinc

import "context"

// Key is one entry in the Chef Server keys API. The same struct serves as the
// GET response, the create/update request body, and the create response. Most
// fields are populated in only a subset of those contexts — they are tagged
// omitempty so requests stay minimal.
type Key struct {
	Name           string `json:"name,omitempty"`
	PublicKey      string `json:"public_key,omitempty"`
	ExpirationDate string `json:"expiration_date,omitempty"` // "infinity" or ISO-8601 UTC

	// CreateKey, when true on a create request, asks the server to generate
	// the keypair. The response will then carry PrivateKey. KeyScope.Create
	// sends it whenever PublicKey is empty.
	CreateKey bool `json:"create_key,omitempty"`

	// PrivateKey is populated only on the response to a server-generated
	// create. It is never sent on a request.
	PrivateKey string `json:"private_key,omitempty"`

	// URI is populated by list and create responses.
	URI string `json:"uri,omitempty"`

	// Expired is populated by list responses.
	Expired bool `json:"expired,omitempty"`
}

// KeysService is the entry point for the Chef Server keys API. The same API
// shape is reachable via two different paths: per-user at /users/USER/keys
// (not org-scoped) and per-client at /organizations/ORG/clients/CLIENT/keys.
// User and Client return scoped handles for each.
type KeysService struct{ client *Client }

// User returns a handle to the keys of the named global user.
func (s *KeysService) User(name string) *KeyScope {
	return &KeyScope{client: s.client, path: "/users/" + esc(name) + "/keys"}
}

// Client returns a handle to the keys of the named org client.
func (s *KeysService) Client(name string) *KeyScope {
	return &KeyScope{
		client: s.client,
		path:   s.client.orgPath("/clients/" + esc(name) + "/keys"),
	}
}

// KeyScope is a handle to one collection of keys (either a user's or a
// client's). Its CRUD methods all operate against that collection.
type KeyScope struct {
	client *Client
	path   string // absolute server path of the keys collection
}

func (s *KeyScope) item(name string) string { return s.path + "/" + esc(name) }

// List returns every key in the scope.
func (s *KeyScope) List(ctx context.Context) ([]Key, *Response, error) {
	return do[[]Key](ctx, s.client, "GET", s.path, nil)
}

// Get fetches a single key by name.
func (s *KeyScope) Get(ctx context.Context, name string) (*Key, *Response, error) {
	k, resp, err := do[Key](ctx, s.client, "GET", s.item(name), nil)
	return ptrOrNil(k, err), resp, err
}

// Create adds a new key. Unless k.PublicKey is set, the server generates the
// keypair and the response's PrivateKey carries the private half: erchef
// requires one of public_key or create_key, so create_key is sent whenever no
// public key is. It also requires expiration_date, so an empty
// k.ExpirationDate is sent as "infinity". k is not modified.
func (s *KeyScope) Create(ctx context.Context, k *Key) (*Key, *Response, error) {
	req := *k
	if req.PublicKey == "" {
		req.CreateKey = true
	}
	if req.ExpirationDate == "" {
		req.ExpirationDate = "infinity"
	}
	created, resp, err := do[Key](ctx, s.client, "POST", s.path, &req)
	return ptrOrNil(created, err), resp, err
}

// Update replaces an existing key by name. To rename a key, set k.Name to the
// new name; the path segment is the existing name.
func (s *KeyScope) Update(ctx context.Context, name string, k *Key) (*Key, *Response, error) {
	updated, resp, err := do[Key](ctx, s.client, "PUT", s.item(name), k)
	return ptrOrNil(updated, err), resp, err
}

// Delete removes a key by name.
func (s *KeyScope) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE", s.item(name), nil)
	return resp, err
}
