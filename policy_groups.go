package cinc

import "context"

// PolicyAssignment is the per-policy value inside a PolicyGroup — it pins one
// policy name to a specific revision id.
type PolicyAssignment struct {
	RevisionID string `json:"revision_id"`
}

// PolicyGroup is the response from GET /policy_groups/NAME. The Policies map
// shows which revision of each policy is currently active in the group.
type PolicyGroup struct {
	URI      string                      `json:"uri,omitempty"`
	Policies map[string]PolicyAssignment `json:"policies,omitempty"`
}

// PolicyGroupsService accesses the /policy_groups endpoints.
type PolicyGroupsService struct{ client *Client }

// List returns every policy group and the policy revisions pinned in it.
func (s *PolicyGroupsService) List(ctx context.Context) (map[string]PolicyGroup, *Response, error) {
	return do[map[string]PolicyGroup](ctx, s.client, "GET",
		s.client.orgPath("/policy_groups"), nil)
}

// Get returns one group's pinned policy revisions.
func (s *PolicyGroupsService) Get(ctx context.Context, name string) (*PolicyGroup, *Response, error) {
	g, resp, err := do[PolicyGroup](ctx, s.client, "GET",
		s.client.orgPath("/policy_groups/"+esc(name)), nil)
	return ptrOrNil(g, err), resp, err
}

// Delete removes a policy group and all of its policy pinnings.
func (s *PolicyGroupsService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/policy_groups/"+esc(name)), nil)
	return resp, err
}

// GetPolicy returns the policy revision currently active in group for the
// named policy.
func (s *PolicyGroupsService) GetPolicy(ctx context.Context, group, policy string) (*PolicyRevision, *Response, error) {
	r, resp, err := do[PolicyRevision](ctx, s.client, "GET",
		s.client.orgPath("/policy_groups/"+esc(group)+"/policies/"+esc(policy)), nil)
	return ptrOrNil(r, err), resp, err
}

// PutPolicy uploads a policy revision and pins it into group as the active
// revision of policy. This is the primary way to associate a revision with a
// group, and creates the group implicitly if it does not exist. The doc body
// may be a *PolicyRevision, a map, or any JSON-marshallable Policyfile.
func (s *PolicyGroupsService) PutPolicy(ctx context.Context, group, policy string, doc any) (*PolicyRevision, *Response, error) {
	r, resp, err := do[PolicyRevision](ctx, s.client, "PUT",
		s.client.orgPath("/policy_groups/"+esc(group)+"/policies/"+esc(policy)), doc)
	return ptrOrNil(r, err), resp, err
}

// DeletePolicy removes the pinning of one policy from a group, without
// deleting the underlying revision.
func (s *PolicyGroupsService) DeletePolicy(ctx context.Context, group, policy string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/policy_groups/"+esc(group)+"/policies/"+esc(policy)), nil)
	return resp, err
}
