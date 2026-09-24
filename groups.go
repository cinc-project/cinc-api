package cinc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// Group is a Chef ACL group. The shape mirrors the server's GET response.
// On Update the Users/Clients/Groups slices are rewrapped into the nested
// "actors" object the server requires on PUT.
type Group struct {
	Name      string   `json:"name,omitempty"`
	GroupName string   `json:"groupname,omitempty"`
	OrgName   string   `json:"orgname,omitempty"`
	Users     []string `json:"users,omitempty"`
	Clients   []string `json:"clients,omitempty"`
	Groups    []string `json:"groups,omitempty"`
}

// UnmarshalJSON decodes a group in either shape the server uses. A GET
// response lists members in top-level users/clients/groups arrays; a PUT body
// (and erchef's reply to a PUT, which echoes it) nests them in an "actors"
// object instead. Members found in both places are merged, without
// duplicates, so a group file written in the PUT shape round-trips through
// Update instead of emptying the group.
//
// When "actors" is an array it is the flat list a GET adds alongside the
// typed lists (clients and users on erchef, every member on cinc-server-ng).
// It cannot be split back by kind and says nothing the typed lists do not, so
// it is ignored.
func (g *Group) UnmarshalJSON(data []byte) error {
	type plain Group // no methods, so no recursion
	var aux struct {
		plain
		Actors json.RawMessage `json:"actors"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*g = Group(aux.plain)
	if !bytes.HasPrefix(bytes.TrimSpace(aux.Actors), []byte("{")) {
		return nil
	}
	var nested struct {
		Users   []string `json:"users"`
		Clients []string `json:"clients"`
		Groups  []string `json:"groups"`
	}
	if err := json.Unmarshal(aux.Actors, &nested); err != nil {
		return fmt.Errorf("cinc: group actors: %w", err)
	}
	addMembers(&g.Users, nested.Users)
	addMembers(&g.Clients, nested.Clients)
	addMembers(&g.Groups, nested.Groups)
	return nil
}

// MemberKind selects one of a group's three member lists.
type MemberKind string

// The member kinds a group holds.
const (
	MemberUser   MemberKind = "user"
	MemberClient MemberKind = "client"
	MemberGroup  MemberKind = "group"
)

// ParseMemberKind reads a member kind from its singular or plural name
// ("user" or "users", and so on).
func ParseMemberKind(s string) (MemberKind, error) {
	switch s {
	case "user", "users":
		return MemberUser, nil
	case "client", "clients":
		return MemberClient, nil
	case "group", "groups":
		return MemberGroup, nil
	}
	return "", fmt.Errorf("cinc: unknown member kind %q: want user, client, or group", s)
}

// members returns the list of g that k selects, for changing in place.
func (k MemberKind) members(g *Group) (*[]string, error) {
	switch k {
	case MemberUser:
		return &g.Users, nil
	case MemberClient:
		return &g.Clients, nil
	case MemberGroup:
		return &g.Groups, nil
	}
	return nil, fmt.Errorf("cinc: unknown member kind %q: want user, client, or group", string(k))
}

// MemberChange reports what AddMembers or RemoveMembers did with each name it
// was given. Every name lands in exactly one list, in the order given, with
// repeats counted once.
type MemberChange struct {
	// Changed names the members the server now has (add) or no longer has
	// (remove), confirmed by reading the group back.
	Changed []string
	// Unchanged names those already as asked before the change: already a
	// member for an add, not a member for a remove. They are not sent.
	Unchanged []string
	// Dropped names the changes the server accepted but did not apply, seen
	// when the group is read back. On an add these are names the server
	// could not resolve: erchef (and cinc-server-ng) answer a PUT naming an
	// actor or group that does not exist with success and silently leave it
	// out. On a remove these are members still present afterwards, which
	// only a concurrent writer re-adding them should cause.
	Dropped []string
}

// GroupsService accesses the /groups endpoints.
type GroupsService struct{ client *Client }

// List returns the group name->URL index.
func (s *GroupsService) List(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET", s.client.orgPath("/groups"), nil)
}

// Get retrieves a single group by name, including its members.
func (s *GroupsService) Get(ctx context.Context, name string) (*Group, *Response, error) {
	g, resp, err := do[Group](ctx, s.client, "GET",
		s.client.orgPath("/groups/"+esc(name)), nil)
	return ptrOrNil(g, err), resp, err
}

// Create creates an empty group with the given name. Use Update to populate
// its members. The server identifies the new group by "groupname" (a "name"
// key is rejected with a 400).
func (s *GroupsService) Create(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "POST",
		s.client.orgPath("/groups"), map[string]string{"groupname": name})
	return resp, err
}

// Update replaces a group's members. The path segment is the group's name,
// taken from Name or, when that is empty, GroupName; the body is emitted in
// the {groupname, actors:{users,clients,groups}} shape the server requires on
// PUT. Nil member slices become empty JSON arrays rather than null.
//
// A group with neither field set is an error rather than a PUT to the
// collection endpoint.
func (s *GroupsService) Update(ctx context.Context, g *Group) (*Group, *Response, error) {
	name := g.name()
	if name == "" {
		return nil, nil, errors.New("cinc: group requires a Name or GroupName to update")
	}
	body := map[string]any{
		"groupname": name,
		"actors": map[string]any{
			"users":   nonNil(g.Users),
			"clients": nonNil(g.Clients),
			"groups":  nonNil(g.Groups),
		},
	}
	updated, resp, err := do[Group](ctx, s.client, "PUT",
		s.client.orgPath("/groups/"+esc(name)), body)
	return ptrOrNil(updated, err), resp, err
}

// name is the group's identifier. Chef identifies a group by "groupname";
// "name" is a courtesy field carrying the same value that not every server
// populates, so prefer it but fall back.
func (g *Group) name() string {
	if g.Name != "" {
		return g.Name
	}
	return g.GroupName
}

// Delete removes a group by name.
func (s *GroupsService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/groups/"+esc(name)), nil)
	return resp, err
}

// nonNil returns s as-is unless s is nil, in which case it returns an empty
// slice so JSON encoding produces [] rather than null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// AddMembers adds names, all of one kind, to group. It reads the group,
// PUTs it back with the missing names added (skipping the PUT when every name
// is already a member), then reads it again to report which names the server
// kept and which it dropped: a name that does not resolve to an existing
// user, client or group is accepted and silently discarded, so a successful
// PUT alone does not mean the member was added. A dropped name is not an
// error; check MemberChange.Dropped.
//
// The update replaces the group's whole membership, so a change another
// writer makes between the read and the PUT is lost; the Chef API offers no
// way to make it conditional.
func (s *GroupsService) AddMembers(ctx context.Context, group string, kind MemberKind, names ...string) (*MemberChange, error) {
	return s.changeMembers(ctx, group, kind, names, true)
}

// RemoveMembers removes names, all of one kind, from group, with the same
// read, PUT and read-back as AddMembers. Names that are not members are
// reported Unchanged, and a PUT is skipped when none are.
func (s *GroupsService) RemoveMembers(ctx context.Context, group string, kind MemberKind, names ...string) (*MemberChange, error) {
	return s.changeMembers(ctx, group, kind, names, false)
}

// changeMembers is AddMembers (add) or RemoveMembers (!add). Every argument
// is checked before the first request.
func (s *GroupsService) changeMembers(ctx context.Context, group string, kind MemberKind, names []string, add bool) (*MemberChange, error) {
	if group == "" {
		return nil, errors.New("cinc: group name must not be empty")
	}
	if _, err := kind.members(&Group{}); err != nil {
		return nil, err
	}
	if slices.Contains(names, "") {
		return nil, errors.New("cinc: member name must not be empty")
	}
	change := &MemberChange{}
	var wanted []string
	addMembers(&wanted, names) // dedupe, keeping first-seen order
	if len(wanted) == 0 {
		return change, nil
	}

	current, _, err := s.Get(ctx, group)
	if err != nil {
		return nil, err
	}
	list, _ := kind.members(current)
	var sent []string
	for _, n := range wanted {
		if slices.Contains(*list, n) == add {
			change.Unchanged = append(change.Unchanged, n)
		} else {
			sent = append(sent, n)
		}
	}
	if len(sent) == 0 {
		return change, nil
	}
	if add {
		addMembers(list, sent)
	} else {
		removeMembers(list, sent)
	}
	current.Name = group
	if _, _, err := s.Update(ctx, current); err != nil {
		return nil, err
	}

	after, _, err := s.Get(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("cinc: group %q was updated, but reading it back failed: %w", group, err)
	}
	final, _ := kind.members(after)
	for _, n := range sent {
		if slices.Contains(*final, n) == add {
			change.Changed = append(change.Changed, n)
		} else {
			change.Dropped = append(change.Dropped, n)
		}
	}
	return change, nil
}
