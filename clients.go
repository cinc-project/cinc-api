package cinc

import (
	"context"
	"errors"
	"fmt"
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

	// ChefKey is populated only on the Create response. It is never sent.
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

// ChefKey holds key material returned when a client is created.
type ChefKey struct {
	Name       string `json:"name,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	ExpiresAt  string `json:"expiration_date,omitempty"`
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
func (s *ClientsService) Create(ctx context.Context, cl *APIClient) (*APIClient, *Response, error) {
	req := clientCreateRequest{
		Name:      cl.Name,
		Validator: cl.Validator,
		PublicKey: cl.PublicKey,
		CreateKey: cl.PublicKey == "",
	}
	created, resp, err := s.res().create(ctx, req)
	return ptrOrNil(created, err), resp, err
}

// Update replaces an existing client's name and validator flag. Key fields on
// cl are not sent; manage keys with Keys.Client(name).
func (s *ClientsService) Update(ctx context.Context, cl *APIClient) (*APIClient, *Response, error) {
	req := clientUpdateRequest{Name: cl.Name, Validator: cl.Validator}
	updated, resp, err := s.res().update(ctx, cl.Name, req)
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

// Reregister regenerates the named client's "default" key, invalidating the
// old private key and returning the new one (in the result's PrivateKey).
//
// The keys API has no in-place regenerate, so this deletes the existing
// "default" key and creates a fresh one with the server generating the pair.
// A client with no default key (the delete 404s) simply gets one created.
// The two calls are not atomic: if the create fails after the delete, the
// client is briefly left without a default key, and the returned error says so
// — recover by adding a key with Keys.Client(name).Create.
func (s *ClientsService) Reregister(ctx context.Context, name string) (*Key, *Response, error) {
	keys := s.client.Keys.Client(name)
	_, err := keys.Delete(ctx, "default")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}
	deleted := err == nil
	created, resp, err := keys.Create(ctx, &Key{Name: "default", CreateKey: true, ExpirationDate: "infinity"})
	if err != nil && !deleted {
		return nil, resp, err
	}
	if err != nil {
		return nil, resp, fmt.Errorf("cinc: reregister %q deleted the old default key but could not create a new one (add one with Keys.Client(%q).Create): %w", name, name, err)
	}
	return created, resp, nil
}
