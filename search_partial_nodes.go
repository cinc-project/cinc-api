package cinc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"path"
	"strings"
	"time"
)

// PartialNode is one row of a partial node search (SearchService.PartialNodes):
// the node's name and the values projected for it, without the rest of the
// node.
//
// Data is keyed by the dotted paths asked for. Each value is read from the
// node's merged attributes, the view the server indexes, so it is the value
// chef-client would see whatever precedence level set it. An attribute the
// node lacks is present with a nil value. Numbers decode as json.Number, so
// large integers and ohai_time keep their exact value.
type PartialNode struct {
	Name string
	URL  string
	Data map[string]any
}

// Value returns the projected value for path, one of the paths passed to
// PartialNodes. It reports false only when path was not projected.
func (p *PartialNode) Value(path string) (any, bool) {
	v, ok := p.Data[path]
	return v, ok
}

// Scalar returns the projected value for path as a string, with the rules of
// Node.AttributeScalar: a string as is, a number or bool formatted, an array
// by its first element. It reports false when path was not projected or the
// value is null, a map, or another non-scalar.
func (p *PartialNode) Scalar(path string) (string, bool) {
	v, ok := p.Data[path]
	if !ok {
		return "", false
	}
	return attributeScalar(v)
}

// LastCheckin is Node.LastCheckin for a partial row: when the node last
// completed a chef-client run, read from the projected "ohai_time". Project
// "ohai_time" to use it; it reports false when that was not projected or the
// node has never checked in.
func (p *PartialNode) LastCheckin() (time.Time, bool) {
	return checkinTime(p.Data["ohai_time"])
}

var errPartialNodesNoPaths = errors.New("cinc: Search.PartialNodes needs at least one attribute path to project")

// PartialNodes returns an iterator over every node matching query, each
// carrying only the attribute paths given (dotted, as in WithPartialPaths;
// "name" and "ohai_time" are paths like any other). It is the partial-search
// counterpart of Nodes, for callers that need a few fields of many nodes,
// as knife status does: the server sends only the projection, not the whole
// node.
//
// opts may add paging (WithRows, WithStart) or further projections
// (WithPartial for a key other than the path). Paging and errors behave as in
// Nodes. With no paths and no WithPartial it yields one error without
// sending a request. A row that is not a partial-search row ends the
// iteration with an error.
func (s *SearchService) PartialNodes(ctx context.Context, query string, paths []string, opts ...SearchOption) iter.Seq2[*PartialNode, error] {
	opts = append([]SearchOption{WithPartialPaths(paths...)}, opts...)
	var p searchParams
	for _, o := range opts {
		o(&p)
	}
	if len(p.partial) == 0 {
		return func(yield func(*PartialNode, error) bool) { yield(nil, errPartialNodesNoPaths) }
	}
	return decodeRows(s.All(ctx, "node", query, opts...), decodePartialNode)
}

func decodePartialNode(row json.RawMessage) (*PartialNode, error) {
	var env struct {
		URL  *string        `json:"url"`
		Data map[string]any `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(row))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil || env.URL == nil || env.Data == nil {
		return nil, fmt.Errorf("cinc: node search row is not a partial-search row: %.200s", row)
	}
	n := &PartialNode{URL: *env.URL, Data: env.Data}
	n.Name = nodeNameFromURL(n.URL)
	if n.Name == "" {
		n.Name, _ = n.Data["name"].(string)
	}
	return n, nil
}

// nodeNameFromURL returns the node name at the end of a node URL
// (.../nodes/<name>), or "" if the URL does not have that shape.
func nodeNameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	dir, name := path.Split(u.Path)
	if !strings.HasSuffix(dir, "/nodes/") || name == "" {
		return ""
	}
	return name
}
