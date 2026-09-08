package cinc

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Node is a Chef node object.
type Node struct {
	Name        string     `json:"name"`
	Environment string     `json:"chef_environment,omitempty"`
	RunList     []string   `json:"run_list"`
	Normal      Attributes `json:"normal,omitempty"`
	Default     Attributes `json:"default,omitempty"`
	Override    Attributes `json:"override,omitempty"`
	Automatic   Attributes `json:"automatic,omitempty"`
	PolicyName  string     `json:"policy_name,omitempty"`
	PolicyGroup string     `json:"policy_group,omitempty"`
}

// Tags returns the node's tags. Chef stores them as a string array under the
// node's normal attributes (normal.tags); this tolerates the JSON-decoded
// shapes that value can take ([]string or []any of strings) and returns nil
// when there are none.
func (n *Node) Tags() []string {
	raw, ok := n.Normal["tags"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return slices.Clone(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// SetTags replaces the node's tags, allocating the normal attribute map if the
// node has none yet.
func (n *Node) SetTags(tags []string) {
	if n.Normal == nil {
		n.Normal = Attributes{}
	}
	n.Normal["tags"] = tags
}

// AddTags adds tags that are not already present, preserving the order of the
// existing tags and appending new ones.
func (n *Node) AddTags(tags ...string) {
	n.SetTags(appendMissing(n.Tags(), tags))
}

// RemoveTags drops the given tags from the node.
func (n *Node) RemoveTags(tags ...string) {
	n.SetTags(without(n.Tags(), tags))
}

// AddRunListItems appends run-list entries that are not already present,
// preserving existing entries and their order.
func (n *Node) AddRunListItems(items ...string) {
	n.RunList = appendMissing(n.RunList, items)
}

// RemoveRunListItems removes the given entries from the node's run list.
func (n *Node) RemoveRunListItems(items ...string) {
	n.RunList = without(n.RunList, items)
}

// Attribute looks up an attribute by name across the node's precedence levels,
// returning the first match in Chef read-precedence order — automatic →
// override → normal → default (automatic, the highest precedence, wins). The
// name may be a dot-separated path into nested attributes
// (e.g. "network.default_gateway").
func (n *Node) Attribute(name string) (any, bool) {
	path := strings.Split(name, ".")
	for _, scope := range []Attributes{n.Automatic, n.Override, n.Normal, n.Default} {
		if scope == nil {
			continue
		}
		if v, ok := scope.Dig(path...); ok {
			return v, true
		}
	}
	return nil, false
}

// AttributeString resolves Attribute and coerces the result to a string: a
// string is returned as-is, a non-empty array yields its first element
// (coerced), and any other value is formatted with fmt.Sprint. It returns ""
// when the attribute is absent. This mirrors how Chef tooling reads a single
// scalar attribute (e.g. fqdn) that may be stored as a one-element array.
func (n *Node) AttributeString(name string) string {
	v, ok := n.Attribute(name)
	if !ok {
		return ""
	}
	return attributeToString(v)
}

func attributeToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		if len(v) == 0 {
			return ""
		}
		return attributeToString(v[0])
	default:
		return fmt.Sprint(v)
	}
}

// appendMissing returns base with every item not already present appended in
// order; existing entries keep their positions.
func appendMissing(base, items []string) []string {
	out := slices.Clone(base)
	for _, item := range items {
		if !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return out
}

// without returns base with every entry equal to one of items removed.
func without(base, items []string) []string {
	out := make([]string, 0, len(base))
	for _, entry := range base {
		if !slices.Contains(items, entry) {
			out = append(out, entry)
		}
	}
	return out
}

// NodesService accesses the /nodes endpoints.
type NodesService struct{ client *Client }

func (s *NodesService) res() crud[Node] {
	return crud[Node]{client: s.client, path: "/nodes"}
}

// Get retrieves a node by name.
func (s *NodesService) Get(ctx context.Context, name string) (*Node, *Response, error) {
	n, resp, err := s.res().get(ctx, name)
	return ptrOrNil(n, err), resp, err
}

// Create creates a new node.
func (s *NodesService) Create(ctx context.Context, n *Node) (*Node, *Response, error) {
	created, resp, err := s.res().create(ctx, n)
	return ptrOrNil(created, err), resp, err
}

// Update replaces an existing node.
func (s *NodesService) Update(ctx context.Context, n *Node) (*Node, *Response, error) {
	updated, resp, err := s.res().update(ctx, n.Name, n)
	return ptrOrNil(updated, err), resp, err
}

// Delete removes a node by name.
func (s *NodesService) Delete(ctx context.Context, name string) (*Response, error) {
	return s.res().remove(ctx, name)
}

// List returns the node name->URL index.
func (s *NodesService) List(ctx context.Context) (map[string]string, *Response, error) {
	return s.res().list(ctx)
}

// ptrOrNil returns &v on success, nil if err != nil.
func ptrOrNil[T any](v T, err error) *T {
	if err != nil {
		return nil
	}
	return &v
}
