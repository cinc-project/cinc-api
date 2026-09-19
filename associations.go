package cinc

import "context"

// OrgUser is an organization member's record as returned by the org-scoped
// /organizations/ORG/users endpoints. It is a projection of the global user
// object limited to the fields the membership API returns.
type OrgUser struct {
	Username    string `json:"username,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
}

// Invitation is a pending organization invitation (association request). The
// org-side listing populates ID and Username; the user-side listing populates
// ID and OrgName.
type Invitation struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	OrgName  string `json:"orgname,omitempty"`
}

// InviteResult is the response from creating an organization invitation.
type InviteResult struct {
	URI string `json:"uri,omitempty"`
}

// AssociationsService manages the link between users and organizations:
// membership at /organizations/ORG/users (distinct from the top-level /users
// handled by UsersService) and invitations at the org-side
// /organizations/ORG/association_requests and the user-side
// /users/USER/association_requests.
type AssociationsService struct{ client *Client }

// orgMember wraps the {"user":{...}} envelope the org membership listing uses.
type orgMember struct {
	User OrgUser `json:"user"`
}

// ListMembers returns the usernames of every user associated with the org.
func (s *AssociationsService) ListMembers(ctx context.Context) ([]string, *Response, error) {
	wrapped, resp, err := do[[]orgMember](ctx, s.client, "GET", s.client.orgPath("/users"), nil)
	if err != nil {
		return nil, resp, err
	}
	names := make([]string, len(wrapped))
	for i, m := range wrapped {
		names[i] = m.User.Username
	}
	return names, resp, nil
}

// GetMember returns one organization member's record.
func (s *AssociationsService) GetMember(ctx context.Context, name string) (*OrgUser, *Response, error) {
	u, resp, err := do[OrgUser](ctx, s.client, "GET", s.client.orgPath("/users/"+esc(name)), nil)
	return ptrOrNil(u, err), resp, err
}

// AddMember immediately associates an existing user with the organization.
// This is a superuser-only operation; most callers should use Invite instead.
func (s *AssociationsService) AddMember(ctx context.Context, username string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "POST", s.client.orgPath("/users"),
		map[string]string{"username": username})
	return resp, err
}

// RemoveMember removes a user's association with the organization and returns
// the user's end state.
func (s *AssociationsService) RemoveMember(ctx context.Context, name string) (*OrgUser, *Response, error) {
	u, resp, err := do[OrgUser](ctx, s.client, "DELETE", s.client.orgPath("/users/"+esc(name)), nil)
	return ptrOrNil(u, err), resp, err
}

// ListInvites returns the organization's pending invitations.
func (s *AssociationsService) ListInvites(ctx context.Context) ([]Invitation, *Response, error) {
	return do[[]Invitation](ctx, s.client, "GET", s.client.orgPath("/association_requests"), nil)
}

// Invite creates an invitation for username to join the organization.
func (s *AssociationsService) Invite(ctx context.Context, username string) (*InviteResult, *Response, error) {
	r, resp, err := do[InviteResult](ctx, s.client, "POST",
		s.client.orgPath("/association_requests"), map[string]string{"user": username})
	return ptrOrNil(r, err), resp, err
}

// RescindInvite cancels a pending organization invitation by its ID.
func (s *AssociationsService) RescindInvite(ctx context.Context, id string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/association_requests/"+esc(id)), nil)
	return resp, err
}

// ListUserInvites returns the invitations pending for the named global user.
// This is the user-side view at /users/USER/association_requests, so the
// invitations carry OrgName rather than Username.
func (s *AssociationsService) ListUserInvites(ctx context.Context, username string) ([]Invitation, *Response, error) {
	return do[[]Invitation](ctx, s.client, "GET", "/users/"+esc(username)+"/association_requests", nil)
}

// UserInviteCount returns the number of invitations pending for the user.
func (s *AssociationsService) UserInviteCount(ctx context.Context, username string) (int, *Response, error) {
	v, resp, err := do[struct {
		Value int `json:"value"`
	}](ctx, s.client, "GET", "/users/"+esc(username)+"/association_requests/count", nil)
	return v.Value, resp, err
}

// RespondInvite accepts (accept=true) or rejects (accept=false) one of the
// user's pending invitations.
func (s *AssociationsService) RespondInvite(ctx context.Context, username, id string, accept bool) (*Response, error) {
	response := "reject"
	if accept {
		response = "accept"
	}
	_, resp, err := do[map[string]any](ctx, s.client, "PUT",
		"/users/"+esc(username)+"/association_requests/"+esc(id), map[string]string{"response": response})
	return resp, err
}

// userOrg wraps the {"organization":{...}} envelope the user-orgs listing uses.
type userOrg struct {
	Organization Org `json:"organization"`
}

// ListUserOrgs returns the organizations the named global user belongs to.
func (s *AssociationsService) ListUserOrgs(ctx context.Context, username string) ([]Org, *Response, error) {
	wrapped, resp, err := do[[]userOrg](ctx, s.client, "GET", "/users/"+esc(username)+"/organizations", nil)
	if err != nil {
		return nil, resp, err
	}
	orgs := make([]Org, len(wrapped))
	for i, w := range wrapped {
		orgs[i] = w.Organization
	}
	return orgs, resp, nil
}
