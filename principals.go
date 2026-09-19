package cinc

import "context"

// Principal is one identity entry returned by the principals endpoint: the
// public key and metadata for a user or client. A single name may resolve to
// more than one principal (e.g. a client and a user sharing a name), which is
// why the endpoint returns a list.
type Principal struct {
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"` // "user" or "client"
	PublicKey string `json:"public_key,omitempty"`
	AuthzID   string `json:"authz_id,omitempty"`
	OrgMember bool   `json:"org_member"`
}

// PrincipalsService accesses /organizations/ORG/principals/NAME, the endpoint
// used to look up the public key(s) and type of a user or client so that a
// request's signature can be verified.
type PrincipalsService struct{ client *Client }

// Get returns the principals (public keys + metadata) registered under name.
func (s *PrincipalsService) Get(ctx context.Context, name string) ([]Principal, *Response, error) {
	out, resp, err := do[struct {
		Principals []Principal `json:"principals"`
	}](ctx, s.client, "GET", s.client.orgPath("/principals/"+esc(name)), nil)
	return out.Principals, resp, err
}
