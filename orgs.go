package cinc

import "context"

// Org is the top-level Chef Server organization. These endpoints live at
// /organizations (not under any one org) and are typically reserved for the
// pivotal superuser identity.
type Org struct {
	Name     string `json:"name,omitempty"`
	FullName string `json:"full_name,omitempty"`
	GUID     string `json:"guid,omitempty"`
}

// OrgCreateResult is the response from POST /organizations. The PrivateKey
// is the generated validator client's RSA key and is returned **only** at
// creation time — store it before the response is dropped.
type OrgCreateResult struct {
	URI        string `json:"uri,omitempty"`
	ClientName string `json:"clientname,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
}

// OrgsService accesses the top-level /organizations endpoints.
type OrgsService struct{ client *Client }

// List returns the name -> URL index of every organization.
func (s *OrgsService) List(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET", "/organizations", nil)
}

// Get retrieves one organization's metadata.
func (s *OrgsService) Get(ctx context.Context, name string) (*Org, *Response, error) {
	o, resp, err := do[Org](ctx, s.client, "GET", "/organizations/"+esc(name), nil)
	return ptrOrNil(o, err), resp, err
}

// Create creates a new organization. The response contains the generated
// validator client name and its private key — capture the key, it is not
// retrievable later.
func (s *OrgsService) Create(ctx context.Context, o *Org) (*OrgCreateResult, *Response, error) {
	r, resp, err := do[OrgCreateResult](ctx, s.client, "POST", "/organizations", o)
	return ptrOrNil(r, err), resp, err
}

// Update replaces an organization's metadata (typically FullName).
func (s *OrgsService) Update(ctx context.Context, o *Org) (*Org, *Response, error) {
	updated, resp, err := do[Org](ctx, s.client, "PUT", "/organizations/"+esc(o.Name), o)
	return ptrOrNil(updated, err), resp, err
}

// Delete removes an organization.
func (s *OrgsService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE", "/organizations/"+esc(name), nil)
	return resp, err
}
