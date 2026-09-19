package cinc

import (
	"context"
	"errors"
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
