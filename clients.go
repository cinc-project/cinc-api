package cinc

import (
	"context"
	"fmt"
)

// APIClient is a Chef API client (node or validator identity). It is named
// APIClient to avoid colliding with the package's own Client type.
type APIClient struct {
	Name      string  `json:"name"`
	Validator bool    `json:"validator"`
	ChefKey   ChefKey `json:"chef_key"`
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

// Create creates a new client and returns its generated key material.
func (s *ClientsService) Create(ctx context.Context, cl *APIClient) (*APIClient, *Response, error) {
	created, resp, err := s.res().create(ctx, cl)
	return ptrOrNil(created, err), resp, err
}

// Update replaces an existing client.
func (s *ClientsService) Update(ctx context.Context, cl *APIClient) (*APIClient, *Response, error) {
	updated, resp, err := s.res().update(ctx, cl.Name, cl)
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
// The two calls are not atomic: if the create fails after the delete, the
// client is briefly left without a default key, and the returned error says so
// — recover by adding a key with Keys.Client(name).Create.
func (s *ClientsService) Reregister(ctx context.Context, name string) (*Key, *Response, error) {
	keys := s.client.Keys.Client(name)
	if _, err := keys.Delete(ctx, "default"); err != nil {
		return nil, nil, err
	}
	created, resp, err := keys.Create(ctx, &Key{Name: "default", CreateKey: true, ExpirationDate: "infinity"})
	if err != nil {
		return nil, resp, fmt.Errorf("cinc: reregister %q deleted the old default key but could not create a new one (add one with Keys.Client(%q).Create): %w", name, name, err)
	}
	return created, resp, nil
}
