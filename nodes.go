package cinc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
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

// MarshalJSON encodes the node with run_list always present as an array.
// Chef's object validator requires an array there, and a nil slice with no
// omitempty would encode as null.
func (n Node) MarshalJSON() ([]byte, error) {
	// The alias has an empty method set, so this does not recurse.
	type alias Node
	a := alias(n)
	a.RunList = nonNil(a.RunList)
	return json.Marshal(a)
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
// node has none yet. Nil becomes an empty array rather than a JSON null, which
// is what Chef stores for a node with no tags.
func (n *Node) SetTags(tags []string) {
	if n.Normal == nil {
		n.Normal = Attributes{}
	}
	n.Normal["tags"] = nonNil(tags)
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

// AddRunListItems appends entries to the node's run list, preserving existing
// entries and their order. The result is normalized the way the Chef Server
// stores it (see NormalizeRunList), so "nginx" is not added beside a stored
// "recipe[nginx]", and bare entries already in the list are qualified.
func (n *Node) AddRunListItems(items ...string) {
	n.RunList = addRunListItems(n.RunList, items)
}

// RemoveRunListItems removes entries from the node's run list, comparing
// normalized forms, so "nginx" removes a stored "recipe[nginx]" and vice
// versa. The remaining list is normalized as well.
func (n *Node) RemoveRunListItems(items ...string) {
	n.RunList = removeRunListItems(n.RunList, items)
}

// EnvironmentName returns the node's chef_environment, or "_default" when it
// is unset, which is the environment the Chef Server assigns such a node.
func (n *Node) EnvironmentName() string {
	if n.Environment == "" {
		return "_default"
	}
	return n.Environment
}

// LastCheckin returns when the node last completed a chef-client run, read
// from automatic.ohai_time (Unix seconds as a float, set by Ohai on each
// run). It accepts the value as any JSON-decoded numeric shape (float64,
// json.Number, int, int64) and reports false when the attribute is absent,
// not a number, or not positive, as on a node that has never checked in.
func (n *Node) LastCheckin() (time.Time, bool) {
	var secs float64
	switch v := n.Automatic["ohai_time"].(type) {
	case float64:
		secs = v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return time.Time{}, false
		}
		secs = f
	case int:
		secs = float64(v)
	case int64:
		secs = float64(v)
	default:
		return time.Time{}, false
	}
	if secs <= 0 {
		return time.Time{}, false
	}
	whole, frac := math.Modf(secs)
	return time.Unix(int64(whole), int64(math.Round(frac*1e9))), true
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

// AttributeString resolves Attribute and coerces the result to a string with
// AttributeScalar's rules, returning "" when the attribute is absent or is not
// a scalar (a map, null, or an array that does not lead with a scalar). This
// mirrors how Chef tooling reads a single scalar attribute (e.g. fqdn) that
// may be stored as a one-element array.
func (n *Node) AttributeString(name string) string {
	s, _ := n.AttributeScalar(name)
	return s
}

// AttributeScalar resolves Attribute and reports its value as a string when it
// is a scalar: a string is returned as-is, a bool or number is formatted (a
// JSON number without an exponent, so 1700000000 stays "1700000000"), and a
// non-empty array yields its first element under the same rules. It returns
// ("", false) when the attribute is absent, null, a map, an empty array, or an
// array whose first element is none of those scalars, so a caller can tell
// "cloud" (an object) from a usable host name instead of receiving Go's
// "map[...]" text.
func (n *Node) AttributeScalar(name string) (string, bool) {
	v, ok := n.Attribute(name)
	if !ok {
		return "", false
	}
	return attributeScalar(v)
}

func attributeScalar(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case json.Number:
		return v.String(), true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32:
		return fmt.Sprint(v), true
	case []any:
		if len(v) == 0 {
			return "", false
		}
		return attributeScalar(v[0])
	default:
		return "", false
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
//
// The Chef Server answers POST /nodes with {"uri":...} rather than the
// created object, so there is nothing to return but the response and any
// error.
func (s *NodesService) Create(ctx context.Context, n *Node) (*Response, error) {
	_, resp, err := s.res().create(ctx, n)
	return resp, err
}

// Update replaces an existing node.
func (s *NodesService) Update(ctx context.Context, n *Node) (*Node, *Response, error) {
	updated, resp, err := s.res().update(ctx, n.Name, n)
	return ptrOrNil(updated, err), resp, err
}

// Modify fetches the named node, applies fn to it, and saves the result,
// returning the node the server holds afterwards and whether a PUT was sent.
//
// When fn leaves the node's JSON encoding unchanged, no PUT is sent and the
// fetched node is returned with false. fn must not rename the node: Modify
// returns an error without saving if Name changed. An error from fn aborts
// Modify before the PUT and is returned as-is. A PUT that fails still reports
// true, since the server may have applied it.
//
// The Chef Server has no optimistic concurrency control on nodes: there is
// no revision or ETag to make the PUT conditional, so a write that lands
// between the GET and the PUT (a chef-client run finishing, say, which saves
// the whole node) is overwritten, and vice versa.
func (s *NodesService) Modify(ctx context.Context, name string, fn func(*Node) error) (*Node, bool, error) {
	if fn == nil {
		return nil, false, errors.New("cinc: Nodes.Modify needs a non-nil fn")
	}
	n, _, err := s.Get(ctx, name)
	if err != nil {
		return nil, false, err
	}
	// A node decoded from JSON always re-encodes, so this cannot fail.
	before, _ := json.Marshal(n)
	if err := fn(n); err != nil {
		return nil, false, err
	}
	if n.Name != name {
		return nil, false, fmt.Errorf("cinc: Nodes.Modify cannot rename node %q to %q; create the new node and delete the old one instead", name, n.Name)
	}
	after, err := json.Marshal(n)
	if err != nil {
		return nil, false, fmt.Errorf("cinc: encoding modified node %q: %w", name, err)
	}
	if bytes.Equal(before, after) {
		return n, false, nil
	}
	updated, _, err := s.Update(ctx, n)
	if err != nil {
		return nil, true, err
	}
	return updated, true, nil
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
