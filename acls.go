package cinc

import (
	"context"
	"fmt"
	"slices"
)

// ACE is a single access-control entry: the actors (users/clients by name)
// and groups granted one permission on one object.
type ACE struct {
	Actors []string `json:"actors"`
	Groups []string `json:"groups"`
}

// AddMembers adds each actor and group to the ACE in place, skipping any that
// are already present, and reports whether the ACE changed. It is the "grant"
// half of an ACL read-modify-write.
func (a *ACE) AddMembers(actors, groups []string) (changed bool) {
	if addMembers(&a.Actors, actors) {
		changed = true
	}
	if addMembers(&a.Groups, groups) {
		changed = true
	}
	return changed
}

// RemoveMembers removes each actor and group from the ACE in place, ignoring
// any that are absent, and reports whether the ACE changed. It is the "revoke"
// half of an ACL read-modify-write.
func (a *ACE) RemoveMembers(actors, groups []string) (changed bool) {
	if removeMembers(&a.Actors, actors) {
		changed = true
	}
	if removeMembers(&a.Groups, groups) {
		changed = true
	}
	return changed
}

// addMembers appends each member missing from list (deduping), reporting
// whether list changed.
func addMembers(list *[]string, members []string) bool {
	changed := false
	for _, m := range members {
		if !slices.Contains(*list, m) {
			*list = append(*list, m)
			changed = true
		}
	}
	return changed
}

// removeMembers deletes each member found in list, reporting whether list
// changed.
func removeMembers(list *[]string, members []string) bool {
	changed := false
	for _, m := range members {
		if i := slices.Index(*list, m); i >= 0 {
			*list = slices.Delete(*list, i, i+1)
			changed = true
		}
	}
	return changed
}

// ACLPerms are the five standard Chef permissions, in the order Chef lists
// them. The pseudo-permission "all" expands to this whole set.
var ACLPerms = []string{"create", "read", "update", "delete", "grant"}

// ExpandPerm turns a permission argument into the concrete permissions it
// targets: "all" expands to every standard permission; a single valid
// permission returns just itself; anything else is an error.
func ExpandPerm(perm string) ([]string, error) {
	if perm == "all" {
		return slices.Clone(ACLPerms), nil
	}
	if slices.Contains(ACLPerms, perm) {
		return []string{perm}, nil
	}
	return nil, fmt.Errorf("cinc: unknown permission %q — want one of create, read, update, delete, grant, or all", perm)
}

// ACL is the complete permission set for a Chef object — five ACEs, one per
// standard Chef permission: create, read, update, delete, grant.
type ACL struct {
	Create ACE `json:"create"`
	Read   ACE `json:"read"`
	Update ACE `json:"update"`
	Delete ACE `json:"delete"`
	Grant  ACE `json:"grant"`
}

// ACEFor returns a pointer to the ACE governing one permission, so a caller can
// read or mutate it in place. perm must be one of the five standard
// permissions; "all" and unknown values are an error.
func (a *ACL) ACEFor(perm string) (*ACE, error) {
	switch perm {
	case "create":
		return &a.Create, nil
	case "read":
		return &a.Read, nil
	case "update":
		return &a.Update, nil
	case "delete":
		return &a.Delete, nil
	case "grant":
		return &a.Grant, nil
	default:
		return nil, errUnknownPerm(perm)
	}
}

// errUnknownPerm is the error for a permission that is not one of the five the
// server serves an endpoint for. "all" is a pseudo-permission understood only
// by ExpandPerm, so it lands here too.
func errUnknownPerm(perm string) error {
	return fmt.Errorf("cinc: unknown permission %q — want one of create, read, update, delete, or grant (expand %q with ExpandPerm)", perm, "all")
}

// ACLsService accesses the per-object ACL endpoints. Every Chef object that
// has identity (nodes, clients, groups, containers, cookbooks, data_bags,
// environments, policies, policy_groups, roles, ...) exposes an _acl
// subresource at the same path shape, so the object kind is a string.
type ACLsService struct{ client *Client }

// Get returns the full five-permission ACL of one object.
//
// objectType is the URL segment that identifies the kind ("nodes",
// "clients", "groups", "containers", "cookbooks", "data", "environments",
// "policies", "policy_groups", "roles", ...).
func (s *ACLsService) Get(ctx context.Context, objectType, name string) (*ACL, *Response, error) {
	return s.getACL(ctx, s.client.orgPath(esc(objectType)+"/"+esc(name)))
}

// SetPermission rewrites one permission's ACE on one object. The Chef API
// requires the request body to wrap the new ACE under the permission name,
// e.g. {"update":{"actors":[],"groups":["admins"]}}.
//
// perm must be one of the five standard permissions. The pseudo-permission
// "all" is rejected: it has no endpoint, so callers expand it with ExpandPerm
// and set each resulting permission.
//
// Nil Actors/Groups slices are coerced to empty arrays so the server does
// not reject the request for a null member list.
func (s *ACLsService) SetPermission(ctx context.Context, objectType, name, perm string, ace *ACE) error {
	return s.setACL(ctx, s.client.orgPath(esc(objectType)+"/"+esc(name)), perm, ace)
}

// GetOrg returns the ACL of the organization object itself, served at
// /organizations/ORG/_acl (no object-type segment).
func (s *ACLsService) GetOrg(ctx context.Context) (*ACL, *Response, error) {
	return s.getACL(ctx, "/organizations/"+esc(s.client.org))
}

// SetOrgPermission rewrites one permission's ACE on the organization object.
func (s *ACLsService) SetOrgPermission(ctx context.Context, perm string, ace *ACE) error {
	return s.setACL(ctx, "/organizations/"+s.client.org, perm, ace)
}

// GetUser returns the ACL of a global user object. User ACLs are top-level
// (/users/USER/_acl), not org-scoped.
func (s *ACLsService) GetUser(ctx context.Context, name string) (*ACL, *Response, error) {
	return s.getACL(ctx, "/users/"+esc(name))
}

// SetUserPermission rewrites one permission's ACE on a global user object.
func (s *ACLsService) SetUserPermission(ctx context.Context, name, perm string, ace *ACE) error {
	return s.setACL(ctx, "/users/"+esc(name), perm, ace)
}

// getACL fetches the full ACL for the object whose path is base (without the
// trailing /_acl segment).
func (s *ACLsService) getACL(ctx context.Context, base string) (*ACL, *Response, error) {
	a, resp, err := do[ACL](ctx, s.client, "GET", base+"/_acl", nil)
	return ptrOrNil(a, err), resp, err
}

// setACL rewrites one permission's ACE on the object whose path is base.
func (s *ACLsService) setACL(ctx context.Context, base, perm string, ace *ACE) error {
	if !slices.Contains(ACLPerms, perm) {
		return errUnknownPerm(perm)
	}
	body := map[string]any{
		perm: map[string]any{
			"actors": nonNil(ace.Actors),
			"groups": nonNil(ace.Groups),
		},
	}
	_, _, err := do[map[string]any](ctx, s.client, "PUT", base+"/_acl/"+esc(perm), body)
	return err
}
