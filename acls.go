package cinc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// removeMembers deletes every occurrence of each member found in list,
// reporting whether list changed.
func removeMembers(list *[]string, members []string) bool {
	before := len(*list)
	*list = slices.DeleteFunc(*list, func(m string) bool { return slices.Contains(members, m) })
	return len(*list) != before
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
	return nil, fmt.Errorf("cinc: unknown permission %q: want one of create, read, update, delete, grant, or all", perm)
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
	return fmt.Errorf("cinc: unknown permission %q: want one of create, read, update, delete, or grant (expand %q with ExpandPerm)", perm, "all")
}

// Object-type URL segments for the org-scoped objects erchef serves an _acl
// endpoint on, for use with ObjectACL. Most are the object's collection name;
// data bags are the exception, served under "data".
const (
	ACLClients           = "clients"
	ACLContainers        = "containers"
	ACLCookbooks         = "cookbooks"
	ACLCookbookArtifacts = "cookbook_artifacts"
	ACLDataBags          = "data"
	ACLEnvironments      = "environments"
	ACLGroups            = "groups"
	ACLNodes             = "nodes"
	ACLPolicies          = "policies"
	ACLPolicyGroups      = "policy_groups"
	ACLRoles             = "roles"
)

// ACLObjectTypes lists every object-type segment above, sorted. A cookbook's
// ACL is shared by all its versions, so it is addressed by name alone, as is a
// cookbook artifact's and a policy's.
var ACLObjectTypes = []string{
	ACLClients, ACLContainers, ACLCookbooks, ACLCookbookArtifacts, ACLDataBags,
	ACLEnvironments, ACLGroups, ACLNodes, ACLPolicies, ACLPolicyGroups, ACLRoles,
}

// aclScope says where an ACL lives.
type aclScope int

const (
	aclObject aclScope = iota + 1 // /organizations/ORG/TYPE/NAME/_acl
	aclOrg                        // /organizations/ORG/organizations/_acl
	aclUser                       // /users/NAME/_acl
)

// ACLTarget identifies the object whose ACL is read or written. Build one with
// ObjectACL, OrgACL or UserACL; the zero value names nothing and every method
// that takes it returns an error. ACLTarget is comparable.
type ACLTarget struct {
	scope      aclScope
	objectType string
	name       string
}

// ObjectACL targets the ACL of one org-scoped object. objectType is its URL
// segment, normally one of the ACL* constants.
func ObjectACL(objectType, name string) ACLTarget {
	return ACLTarget{scope: aclObject, objectType: objectType, name: name}
}

// OrgACL targets the ACL of the organization object itself (the client's
// configured org), served at /organizations/ORG/organizations/_acl.
func OrgACL() ACLTarget { return ACLTarget{scope: aclOrg} }

// UserACL targets the ACL of a global user. User ACLs are top-level
// (/users/NAME/_acl), not org-scoped, and hold users only: erchef refuses a
// client in them.
func UserACL(name string) ACLTarget { return ACLTarget{scope: aclUser, name: name} }

// ObjectType returns the target's URL segment: the object type for ObjectACL,
// "organizations" for OrgACL and "users" for UserACL.
func (t ACLTarget) ObjectType() string {
	switch t.scope {
	case aclOrg:
		return "organizations"
	case aclUser:
		return "users"
	default:
		return t.objectType
	}
}

// Name returns the object's name, or "" for OrgACL.
func (t ACLTarget) Name() string { return t.name }

// String renders the target for messages: "nodes/web01", "organization" or
// "users/alice".
func (t ACLTarget) String() string {
	switch t.scope {
	case aclObject, aclUser:
		return t.ObjectType() + "/" + t.name
	case aclOrg:
		return "organization"
	default:
		return "(no ACL target)"
	}
}

// base returns the path of the target object, without the trailing /_acl.
func (t ACLTarget) base(c *Client) (string, error) {
	switch {
	case t.scope == aclOrg:
		return c.orgPath("organizations"), nil
	case t.scope == aclUser && t.name != "":
		return "/users/" + esc(t.name), nil
	case t.scope == aclObject && t.objectType != "" && t.name != "":
		return c.orgPath(esc(t.objectType) + "/" + esc(t.name)), nil
	default:
		return "", fmt.Errorf("cinc: incomplete ACL target %s: build one with ObjectACL, OrgACL or UserACL, with a name", t)
	}
}

// ACLsService accesses the per-object ACL endpoints. Every Chef object that
// has identity exposes an _acl subresource; ACLTarget says which one.
type ACLsService struct{ client *Client }

// GetTarget returns the full five-permission ACL of the target. Each ACE's
// Actors lists the clients and users together, as erchef reports them.
func (s *ACLsService) GetTarget(ctx context.Context, t ACLTarget) (*ACL, *Response, error) {
	base, err := t.base(s.client)
	if err != nil {
		return nil, nil, err
	}
	return s.getACL(ctx, base)
}

// SetTargetPermission rewrites one permission's ACE on the target. The Chef
// API requires the request body to wrap the new ACE under the permission name,
// e.g. {"update":{"actors":[],"groups":["admins"]}}.
//
// perm must be one of the five standard permissions. The pseudo-permission
// "all" is rejected: it has no endpoint, so callers expand it with ExpandPerm
// and set each resulting permission, or use Grant and Revoke.
//
// Nil Actors/Groups slices are coerced to empty arrays so the server does
// not reject the request for a null member list. The server refuses a member
// that does not exist with a 400 (ErrBadRequest).
func (s *ACLsService) SetTargetPermission(ctx context.Context, t ACLTarget, perm string, ace *ACE) error {
	base, err := t.base(s.client)
	if err != nil {
		return err
	}
	return s.setACL(ctx, base, perm, ace)
}

// Get returns the full ACL of one org-scoped object. It is
// GetTarget(ctx, ObjectACL(objectType, name)).
func (s *ACLsService) Get(ctx context.Context, objectType, name string) (*ACL, *Response, error) {
	return s.GetTarget(ctx, ObjectACL(objectType, name))
}

// SetPermission rewrites one permission's ACE on one org-scoped object. It is
// SetTargetPermission(ctx, ObjectACL(objectType, name), perm, ace).
func (s *ACLsService) SetPermission(ctx context.Context, objectType, name, perm string, ace *ACE) error {
	return s.SetTargetPermission(ctx, ObjectACL(objectType, name), perm, ace)
}

// GetOrg returns the ACL of the organization object itself. It is
// GetTarget(ctx, OrgACL()).
func (s *ACLsService) GetOrg(ctx context.Context) (*ACL, *Response, error) {
	return s.GetTarget(ctx, OrgACL())
}

// SetOrgPermission rewrites one permission's ACE on the organization object.
// It is SetTargetPermission(ctx, OrgACL(), perm, ace).
func (s *ACLsService) SetOrgPermission(ctx context.Context, perm string, ace *ACE) error {
	return s.SetTargetPermission(ctx, OrgACL(), perm, ace)
}

// GetUser returns the ACL of a global user object. It is
// GetTarget(ctx, UserACL(name)).
func (s *ACLsService) GetUser(ctx context.Context, name string) (*ACL, *Response, error) {
	return s.GetTarget(ctx, UserACL(name))
}

// SetUserPermission rewrites one permission's ACE on a global user object. It
// is SetTargetPermission(ctx, UserACL(name), perm, ace).
func (s *ACLsService) SetUserPermission(ctx context.Context, name, perm string, ace *ACE) error {
	return s.SetTargetPermission(ctx, UserACL(name), perm, ace)
}

// ACLChangeError reports a Grant or Revoke that stopped part way. The server
// has no multi-permission write, so each permission is its own PUT: the ones
// in Changed were applied before the write of Perm failed, and the ones after
// Perm were not attempted. Err is the server's error, so errors.Is(err,
// ErrBadRequest) and friends see through it.
type ACLChangeError struct {
	Target  ACLTarget
	Perm    string   // the permission whose write failed
	Changed []string // permissions already written, in ACLPerms order
	Err     error
}

func (e *ACLChangeError) Error() string {
	msg := fmt.Sprintf("cinc: set %s permission on %s: %v", e.Perm, e.Target, e.Err)
	if len(e.Changed) > 0 {
		msg += " (already changed: " + strings.Join(e.Changed, ", ") + ")"
	}
	return msg
}

func (e *ACLChangeError) Unwrap() error { return e.Err }

// Grant adds actors (users and clients, by name) and groups to perm on the
// target, where perm is one of ACLPerms or "all". It reads the ACL once and
// writes only the permissions the members were missing from, returning those
// in ACLPerms order; nil means every member already held perm and nothing was
// sent.
//
// A failed read is returned unchanged, with nothing written. A failed write
// returns the permissions already changed and an *ACLChangeError naming the
// one that failed. A member that does not exist is refused with a 400
// (ErrBadRequest).
//
// Grant is a read-modify-write: a concurrent change to the same permission
// between the read and the write is overwritten.
func (s *ACLsService) Grant(ctx context.Context, t ACLTarget, perm string, actors, groups []string) (changed []string, err error) {
	return s.change(ctx, t, perm, actors, groups, (*ACE).AddMembers)
}

// Revoke removes actors and groups from perm on the target, where perm is one
// of ACLPerms or "all". It follows Grant's rules: one read, a write for each
// permission that held one of the members, and the permissions changed
// returned in order (nil if none held any).
//
// erchef refuses to take the admins group off the grant permission unless the
// caller is the superuser, with a 403 (ErrForbidden) that has nothing to do
// with the caller's own grant permission.
func (s *ACLsService) Revoke(ctx context.Context, t ACLTarget, perm string, actors, groups []string) (changed []string, err error) {
	return s.change(ctx, t, perm, actors, groups, (*ACE).RemoveMembers)
}

// change is the read-modify-write behind Grant and Revoke. edit applies the
// member change to one ACE and reports whether it changed anything.
func (s *ACLsService) change(ctx context.Context, t ACLTarget, perm string, actors, groups []string,
	edit func(*ACE, []string, []string) bool) ([]string, error) {
	perms, err := ExpandPerm(perm)
	if err != nil {
		return nil, err
	}
	if len(actors) == 0 && len(groups) == 0 {
		return nil, errors.New("cinc: an ACL change needs at least one actor or group")
	}
	acl, _, err := s.GetTarget(ctx, t)
	if err != nil {
		return nil, err
	}
	base, _ := t.base(s.client) // GetTarget has already validated t
	var changed []string
	for _, p := range perms {
		ace, _ := acl.ACEFor(p) // p comes from ExpandPerm, so it is valid
		if !edit(ace, actors, groups) {
			continue
		}
		if err := s.setACL(ctx, base, p, ace); err != nil {
			return changed, &ACLChangeError{Target: t, Perm: p, Changed: slices.Clone(changed), Err: err}
		}
		changed = append(changed, p)
	}
	return changed, nil
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
