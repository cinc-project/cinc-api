package cinc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ACE is a single access-control entry: the actors and groups granted one
// permission on one object.
//
// Actors are named in one of two forms, as erchef reports and accepts them:
//
//   - granular: Users and Clients hold the names apart, and Actors is empty.
//     ACLsService.Get asks for this form (?detail=granular), so a user and a
//     client that share a name stay two members.
//   - flat: Actors holds users and clients together, and Users and Clients
//     are empty. A server that ignores detail=granular answers this way.
//
// SetPermission writes the granular form when Users or Clients is non-empty
// (Actors must then be empty: erchef refuses names in both) and the flat form
// otherwise. A flat-form name that is both a client and a member user of the
// org is refused by erchef with a 422, since it cannot tell which was meant.
type ACE struct {
	Actors  []string `json:"actors"`
	Users   []string `json:"users,omitempty"`
	Clients []string `json:"clients,omitempty"`
	Groups  []string `json:"groups"`
}

// ACLMembers names the users, clients and groups an ACL change adds or
// removes. Naming each kind separately is what lets a user and a client that
// share a name be granted and revoked independently.
type ACLMembers struct {
	Users   []string
	Clients []string
	Groups  []string
}

// empty reports whether m names no member at all.
func (m ACLMembers) empty() bool {
	return len(m.Users) == 0 && len(m.Clients) == 0 && len(m.Groups) == 0
}

// AddMembers adds each of m's users, clients and groups to the ACE in place,
// skipping any already present, and reports whether the ACE changed. It is
// the "grant" half of an ACL read-modify-write. It does not touch Actors.
func (a *ACE) AddMembers(m ACLMembers) (changed bool) {
	for _, c := range []bool{
		addMembers(&a.Users, m.Users),
		addMembers(&a.Clients, m.Clients),
		addMembers(&a.Groups, m.Groups),
	} {
		changed = changed || c
	}
	return changed
}

// RemoveMembers removes each of m's users, clients and groups from the ACE in
// place, ignoring any that are absent, and reports whether the ACE changed.
// It is the "revoke" half of an ACL read-modify-write. It does not touch
// Actors.
func (a *ACE) RemoveMembers(m ACLMembers) (changed bool) {
	for _, c := range []bool{
		removeMembers(&a.Users, m.Users),
		removeMembers(&a.Clients, m.Clients),
		removeMembers(&a.Groups, m.Groups),
	} {
		changed = changed || c
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

// aclPerms are the five standard Chef permissions, in the order Chef lists
// them. It is an unexported array so no caller can change it; ACLPerms hands
// out copies.
var aclPerms = [...]string{"create", "read", "update", "delete", "grant"}

// ACLPerms returns the five standard Chef permissions, in the order Chef
// lists them: create, read, update, delete, grant. The pseudo-permission
// "all" expands to this whole set. Each call returns a fresh slice.
func ACLPerms() []string { return slices.Clone(aclPerms[:]) }

// ExpandPerm turns a permission argument into the concrete permissions it
// targets: "all" expands to every standard permission; a single valid
// permission returns just itself; anything else is an error.
func ExpandPerm(perm string) ([]string, error) {
	if perm == "all" {
		return ACLPerms(), nil
	}
	if slices.Contains(aclPerms[:], perm) {
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

// aclObjectTypes lists every object-type segment above, sorted.
var aclObjectTypes = [...]string{
	ACLClients, ACLContainers, ACLCookbooks, ACLCookbookArtifacts, ACLDataBags,
	ACLEnvironments, ACLGroups, ACLNodes, ACLPolicies, ACLPolicyGroups, ACLRoles,
}

// ACLObjectTypes returns every object-type segment above, sorted. A
// cookbook's ACL is shared by all its versions, so it is addressed by name
// alone, as is a cookbook artifact's and a policy's. Each call returns a
// fresh slice.
func ACLObjectTypes() []string { return slices.Clone(aclObjectTypes[:]) }

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

// Get returns the full five-permission ACL of the target. It asks for the
// granular form (?detail=granular, oc_chef_wm_acl:to_json), so each ACE lists
// users and clients apart in Users and Clients, with Actors empty; a server
// that ignores the request fills Actors instead (see ACE).
func (s *ACLsService) Get(ctx context.Context, t ACLTarget) (*ACL, *Response, error) {
	base, err := t.base(s.client)
	if err != nil {
		return nil, nil, err
	}
	a, resp, err := do[ACL](ctx, s.client, "GET", base+"/_acl?detail=granular", nil)
	return ptrOrNil(a, err), resp, err
}

// SetPermission rewrites one permission's ACE on the target. The Chef API
// requires the request body to wrap the new ACE under the permission name,
// e.g. {"update":{"actors":[],"groups":["admins"]}}.
//
// perm must be one of the five standard permissions. The pseudo-permission
// "all" is rejected: it has no endpoint, so callers expand it with ExpandPerm
// and set each resulting permission, or use Grant and Revoke.
//
// When ace has Users or Clients, the body is the granular form, with both
// lists and "actors": []; ace must not then also have Actors. Otherwise it is
// the flat form, Actors and Groups. Nil lists are sent as empty arrays, since
// the server rejects a null member list. The server refuses a member that
// does not exist with a 400 (ErrBadRequest).
func (s *ACLsService) SetPermission(ctx context.Context, t ACLTarget, perm string, ace *ACE) (*Response, error) {
	base, err := t.base(s.client)
	if err != nil {
		return nil, err
	}
	return s.setACL(ctx, base, perm, ace)
}

// ACLChangeError reports a Grant or Revoke that stopped part way. The server
// has no multi-permission write, so each permission is its own PUT: the ones
// in Changed were applied before the write of Perm failed, and the ones after
// Perm were not attempted. Err is the server's error, so errors.Is(err,
// ErrBadRequest) and friends see through it.
type ACLChangeError struct {
	Target  ACLTarget
	Perm    string   // the permission whose write failed
	Changed []string // permissions already written, in ACLPerms() order
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

// Grant adds m's users, clients and groups to perm on the target, where perm
// is one of ACLPerms() or "all". It reads the ACL once and writes only the
// permissions the members were missing from, returning those in ACLPerms()
// order; nil means every member already held perm and nothing was sent.
//
// A failed read is returned unchanged, with nothing written. A failed write
// returns the permissions already changed and an *ACLChangeError naming the
// one that failed. A member that does not exist is refused with a 400
// (ErrBadRequest), as is a client on a UserACL target, which holds users only.
//
// Grant needs the server to report the ACL in the granular form; an ACL read
// back with untyped Actors is refused before anything is written.
//
// Grant is a read-modify-write: a concurrent change to the same permission
// between the read and the write is overwritten.
func (s *ACLsService) Grant(ctx context.Context, t ACLTarget, perm string, m ACLMembers) (changed []string, err error) {
	return s.change(ctx, t, perm, m, (*ACE).AddMembers)
}

// Revoke removes m's users, clients and groups from perm on the target, where
// perm is one of ACLPerms() or "all". It follows Grant's rules: one read, a
// write for each permission that held one of the members, and the permissions
// changed returned in order (nil if none held any). Revoking a client leaves
// a user of the same name granted, and the reverse.
//
// erchef refuses to take the admins group off the grant permission unless the
// caller is the superuser, with a 403 that has nothing to do with the
// caller's own grant permission. Revoke reports that refusal as an error
// matching both ErrAdminsRequiredOnGrant and ErrForbidden.
func (s *ACLsService) Revoke(ctx context.Context, t ACLTarget, perm string, m ACLMembers) (changed []string, err error) {
	return s.change(ctx, t, perm, m, (*ACE).RemoveMembers)
}

// adminsRequiredOnGrant wraps the server's 403 for a write that dropped the
// admins group from grant, so it matches ErrAdminsRequiredOnGrant as well as
// everything the server error matches.
type adminsRequiredOnGrant struct{ err error }

func (e adminsRequiredOnGrant) Error() string {
	return e.err.Error() + " (only the superuser may remove the admins group from grant)"
}

func (e adminsRequiredOnGrant) Unwrap() []error { return []error{ErrAdminsRequiredOnGrant, e.err} }

// change is the read-modify-write behind Grant and Revoke. edit applies the
// member change to one ACE and reports whether it changed anything.
func (s *ACLsService) change(ctx context.Context, t ACLTarget, perm string, m ACLMembers,
	edit func(*ACE, ACLMembers) bool) ([]string, error) {
	perms, err := ExpandPerm(perm)
	if err != nil {
		return nil, err
	}
	if m.empty() {
		return nil, errors.New("cinc: an ACL change needs at least one user, client or group")
	}
	acl, _, err := s.Get(ctx, t)
	if err != nil {
		return nil, err
	}
	// Untyped actors can be neither matched against m nor written back beside
	// users and clients, so refuse before writing anything.
	for _, p := range perms {
		ace, _ := acl.ACEFor(p) // p comes from ExpandPerm, so it is valid
		if len(ace.Actors) > 0 {
			return nil, fmt.Errorf("cinc: the server reported untyped actors for %s on %s: "+
				"Grant and Revoke need an ACL read with detail=granular", p, t)
		}
	}
	base, _ := t.base(s.client) // Get has already validated t
	var changed []string
	for _, p := range perms {
		ace, _ := acl.ACEFor(p)
		hadAdmins := slices.Contains(ace.Groups, "admins")
		if !edit(ace, m) {
			continue
		}
		if _, err := s.setACL(ctx, base, p, ace); err != nil {
			// erchef checks this constraint before authorization, so a 403
			// for a write that drops admins from grant is the constraint,
			// whatever the caller's own permissions.
			if p == "grant" && hadAdmins && !slices.Contains(ace.Groups, "admins") && errors.Is(err, ErrForbidden) {
				err = adminsRequiredOnGrant{err}
			}
			return changed, &ACLChangeError{Target: t, Perm: p, Changed: slices.Clone(changed), Err: err}
		}
		changed = append(changed, p)
	}
	return changed, nil
}

// setACL rewrites one permission's ACE on the object whose path is base, in
// the form SetPermission documents. erchef's acl_spec requires "actors" and
// "groups" on every write, and validate_actors_clients_users requires "users"
// and "clients" together, with "actors" then [] (oc_chef_authz_acl.erl:51-90).
func (s *ACLsService) setACL(ctx context.Context, base, perm string, ace *ACE) (*Response, error) {
	if !slices.Contains(aclPerms[:], perm) {
		return nil, errUnknownPerm(perm)
	}
	if ace == nil {
		return nil, errors.New("cinc: SetPermission needs an ACE, not nil")
	}
	body := map[string]any{
		"actors": nonNil(ace.Actors),
		"groups": nonNil(ace.Groups),
	}
	if len(ace.Users) > 0 || len(ace.Clients) > 0 {
		if len(ace.Actors) > 0 {
			return nil, fmt.Errorf("cinc: the %s ACE names Actors as well as Users or Clients: "+
				"put every name in Users or Clients and leave Actors empty", perm)
		}
		body["users"] = nonNil(ace.Users)
		body["clients"] = nonNil(ace.Clients)
	}
	_, resp, err := do[map[string]any](ctx, s.client, "PUT", base+"/_acl/"+esc(perm), map[string]any{perm: body})
	return resp, err
}
