package cinc

import (
	"context"
	"errors"
)

// APIClient is a Chef API client (node or validator identity). It is named
// APIClient to avoid colliding with the package's own Client type.
type APIClient struct {
	Name      string `json:"name"`
	Validator bool   `json:"validator"`

	// PublicKey, if set on Create, registers this PEM public key as the
	// client's "default" key instead of having the server generate a keypair.
	// Only Create sends it; Update does not, because the server rejects key
	// fields on update — manage keys with Keys.Client(name).
	PublicKey string `json:"public_key,omitempty"`
}

// ClientCreateResult is the response from POST /clients: the new client's URI
// and its "default" key. Unless the APIClient carried a PublicKey,
// ChefKey.PrivateKey holds the server-generated private key. The server keeps
// no copy: capture it before the response is dropped.
type ClientCreateResult struct {
	URI     string  `json:"uri,omitempty"`
	ChefKey ChefKey `json:"chef_key,omitzero"`
}

// clientCreateRequest is the POST /clients body. Under server API v1 a client
// created with neither create_key nor public_key gets no key at all, so
// create_key is sent whenever the caller did not supply a public key.
type clientCreateRequest struct {
	Name      string `json:"name"`
	Validator bool   `json:"validator"`
	PublicKey string `json:"public_key,omitempty"`
	CreateKey bool   `json:"create_key,omitempty"`
}

// clientUpdateRequest is the PUT /clients/NAME body. Key fields are left out:
// the server rejects them on update with key_management_not_supported.
type clientUpdateRequest struct {
	Name      string `json:"name"`
	Validator bool   `json:"validator"`
}

// ClientsService accesses the /clients endpoints.
type ClientsService struct{ client *Client }

func (s *ClientsService) res() crud[APIClient] {
	return crud[APIClient]{client: s.client, path: "/clients"}
}

// Get retrieves a client by name.
func (s *ClientsService) Get(ctx context.Context, name string) (*APIClient, *Response, error) {
	cl, resp, err := s.res().get(ctx, name)
	return ptrOrNil(cl, err), resp, err
}

// Create creates a new client. Unless cl.PublicKey is set, the server generates
// the client's "default" keypair and the result's ChefKey.PrivateKey carries
// the private half — the only chance to capture it.
func (s *ClientsService) Create(ctx context.Context, cl *APIClient) (*ClientCreateResult, *Response, error) {
	req := clientCreateRequest{
		Name:      cl.Name,
		Validator: cl.Validator,
		PublicKey: cl.PublicKey,
		CreateKey: cl.PublicKey == "",
	}
	r, resp, err := doCreate[ClientCreateResult](ctx, s.client, s.res().coll(), cl.Name, req)
	return ptrOrNil(r, err), resp, err
}

// Update replaces the named client's name and validator flag with cl's and
// returns the client as stored. To rename the client, set cl.Name to the new
// name: the server then answers 201 with the new location. An empty cl.Name
// keeps the current one. Key fields on cl are not sent; manage keys with
// Keys.Client(name).
//
// Chef Server keeps no keys across a client rename: the renamed client has
// none and cannot authenticate until it gets one, for example from
// Reregister, which creates a default key for a client that has none.
func (s *ClientsService) Update(ctx context.Context, name string, cl *APIClient) (*APIClient, *Response, error) {
	if name == "" {
		return nil, nil, errors.New("cinc: Clients.Update needs the client's current name")
	}
	req := clientUpdateRequest{Name: cl.Name, Validator: cl.Validator}
	if req.Name == "" {
		req.Name = name
	}
	updated, resp, err := s.res().update(ctx, name, req)
	return ptrOrNil(updated, err), resp, err
}

// Delete removes a client by name.
func (s *ClientsService) Delete(ctx context.Context, name string) (*Response, error) {
	return s.res().remove(ctx, name)
}

// List returns the client name->URL index.
func (s *ClientsService) List(ctx context.Context) (map[string]string, *Response, error) {
	return s.res().list(ctx)
}

// Reregister regenerates the named client's "default" key in place,
// invalidating the old private key, and returns the new pair (PrivateKey and
// PublicKey). The key keeps its expiration date. A client with no default key
// gets one created, with no expiration.
//
// The server will not regenerate the key a request is signed with, so a client
// cannot reregister itself; it answers 403 and the key is unchanged.
func (s *ClientsService) Reregister(ctx context.Context, name string) (*Key, *Response, error) {
	keys := s.client.Keys.Client(name)
	k, resp, err := keys.Update(ctx, "default", &Key{Name: "default", CreateKey: true})
	if errors.Is(err, ErrNotFound) {
		return keys.Create(ctx, &Key{Name: "default"})
	}
	return k, resp, err
}
