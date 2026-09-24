package cinc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"strconv"
	"strings"
)

// SearchResult is one page of search results.
type SearchResult struct {
	Total int               `json:"total"`
	Start int               `json:"start"`
	Rows  []json.RawMessage `json:"rows"`
}

// searchParams accumulates optional search parameters.
type searchParams struct {
	start   int
	rows    int
	partial map[string][]string
}

// SearchOption customizes a search query.
type SearchOption func(*searchParams)

// WithStart sets the result offset.
func WithStart(n int) SearchOption { return func(p *searchParams) { p.start = n } }

// WithRows sets the page size.
func WithRows(n int) SearchOption { return func(p *searchParams) { p.rows = n } }

// WithPartial requests a partial search: each row comes back as
// {"url": ..., "data": {key: value}}, with each value read from the object at
// its attribute path (key -> path). Node paths address the merged attributes
// (["kernel", "release"], not ["automatic", "kernel", "release"]), because
// the server projects from the same merged view it indexes. Use
// UnwrapSearchRow to get at the projected data.
//
// Projection options accumulate: WithPartial and WithPartialPaths each add
// their keys to the projection, and a later option replaces an earlier one's
// path for the same key. The caller's map is copied, never modified.
func WithPartial(keys map[string][]string) SearchOption {
	return func(p *searchParams) {
		for k, path := range keys {
			p.addPartial(k, path)
		}
	}
}

// WithPartialPaths requests a partial search of dotted attribute paths. Each
// path is its own result key, split on "." into the attribute path, so
// WithPartialPaths("kernel.release") projects {"kernel.release": ["kernel",
// "release"]}. An empty string is skipped. It composes with WithPartial as
// described there; use WithPartial for a key other than the path itself or
// for an attribute name that contains a dot.
func WithPartialPaths(paths ...string) SearchOption {
	return func(p *searchParams) {
		for _, path := range paths {
			if path != "" {
				p.addPartial(path, strings.Split(path, "."))
			}
		}
	}
}

func (p *searchParams) addPartial(key string, path []string) {
	if p.partial == nil {
		p.partial = map[string][]string{}
	}
	p.partial[key] = path
}

// UnwrapSearchRow returns the object a search row describes. Search returns
// two kinds of row that wrap the object in an envelope:
//
//   - A partial search row (WithPartial, WithPartialPaths) is an object with
//     exactly two keys, a string "url" and an object "data"; the projection
//     is "data".
//   - A full search of a data bag index returns each item as a
//     Chef::DataBagItem, {"name": "data_bag_item_<bag>_<id>", "json_class":
//     "Chef::DataBagItem", "chef_type": "data_bag_item", "data_bag": <bag>,
//     "raw_data": {...}}; the item is "raw_data". A row is taken to be one
//     when it has that json_class, that chef_type and an object "raw_data".
//
// Any other row (a full node, role, client or environment, or anything that
// is not an object) is returned unchanged. Neither test misfires on a real
// object: every indexed object has more keys than url and data (nodes, roles,
// clients and environments a name, a data bag item an id), and the server
// unwraps a Chef::DataBagItem envelope on write, so no stored item keeps one.
func UnwrapSearchRow(row json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(row, &m) != nil {
		return row
	}
	if len(m) == 2 && isJSONString(m["url"]) && isJSONObject(m["data"]) {
		return m["data"]
	}
	if jsonStringIs(m["json_class"], "Chef::DataBagItem") &&
		jsonStringIs(m["chef_type"], "data_bag_item") && isJSONObject(m["raw_data"]) {
		return m["raw_data"]
	}
	return row
}

func isJSONObject(v json.RawMessage) bool { return bytes.HasPrefix(bytes.TrimSpace(v), []byte("{")) }
func isJSONString(v json.RawMessage) bool { return bytes.HasPrefix(bytes.TrimSpace(v), []byte(`"`)) }

func jsonStringIs(v json.RawMessage, want string) bool {
	var s string
	return json.Unmarshal(v, &s) == nil && s == want
}

// SearchService accesses the /search endpoints.
type SearchService struct{ client *Client }

// Indexes returns the name->URL index of available search indexes (node,
// role, client, environment, and one per data bag).
func (s *SearchService) Indexes(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET", s.client.orgPath("/search"), nil)
}

// Query runs a single search against index with the given query string.
func (s *SearchService) Query(ctx context.Context, index, query string, opts ...SearchOption) (*SearchResult, *Response, error) {
	p := searchParams{rows: 1000}
	for _, o := range opts {
		o(&p)
	}
	v := url.Values{}
	v.Set("q", query)
	v.Set("start", strconv.Itoa(p.start))
	v.Set("rows", strconv.Itoa(p.rows))
	path := s.client.orgPath("/search/"+esc(index)) + "?" + v.Encode()

	var body any
	method := "GET"
	if len(p.partial) > 0 {
		method, body = "POST", p.partial
	}
	res, resp, err := do[SearchResult](ctx, s.client, method, path, body)
	return ptrOrNil(res, err), resp, err
}

// All returns an iterator over every row matching query, fetching one page
// at a time so only the current page is held in memory:
//
//	for row, err := range c.Search.All(ctx, "node", "*:*") {
//		if err != nil {
//			return err
//		}
//		// decode row
//	}
//
// Breaking out of the loop stops further page requests. A failed page
// request (including ctx cancellation) is yielded once as a nil row with a
// non-nil error, after which iteration ends. Options and paging behave as in
// SearchAll.
func (s *SearchService) All(ctx context.Context, index, query string, opts ...SearchOption) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		err := s.eachPage(ctx, index, query, opts, func(res *SearchResult, _ int) bool {
			for _, row := range res.Rows {
				if !yield(row, nil) {
					return false
				}
			}
			return true
		})
		if err != nil {
			yield(nil, err)
		}
	}
}

// errNodesPartial is returned by Nodes when asked for a partial search.
var errNodesPartial = errors.New("cinc: Search.Nodes returns whole nodes, so it can't take a partial search (WithPartial/WithPartialPaths); use Search.All with UnwrapSearchRow instead")

// Nodes returns an iterator over every node matching query, decoded as a
// *Node. It pages like All and ends the same way on a failed page, and also
// ends on a row that does not decode as a node, yielding that error.
//
// A partial search returns projections rather than nodes, so Nodes refuses
// WithPartial and WithPartialPaths: it yields one error without sending a
// request.
func (s *SearchService) Nodes(ctx context.Context, query string, opts ...SearchOption) iter.Seq2[*Node, error] {
	var p searchParams
	for _, o := range opts {
		o(&p)
	}
	if len(p.partial) > 0 {
		return func(yield func(*Node, error) bool) { yield(nil, errNodesPartial) }
	}
	return decodeRows(s.All(ctx, "node", query, opts...), func(row json.RawMessage) (*Node, error) {
		var n Node
		if err := json.Unmarshal(row, &n); err != nil {
			return nil, fmt.Errorf("cinc: decode node search row: %w", err)
		}
		return &n, nil
	})
}

// DataBagItems returns an iterator over every item in data bag bag matching
// query. Each row is passed through UnwrapSearchRow, so a full search yields
// the items themselves rather than their Chef::DataBagItem envelopes, and a
// partial search yields each item's projected keys (which hold the id only
// if the projection asks for it). Paging and errors behave as in Nodes.
func (s *SearchService) DataBagItems(ctx context.Context, bag, query string, opts ...SearchOption) iter.Seq2[DataBagItem, error] {
	return decodeRows(s.All(ctx, bag, query, opts...), func(row json.RawMessage) (DataBagItem, error) {
		var item DataBagItem
		if err := json.Unmarshal(UnwrapSearchRow(row), &item); err != nil {
			return nil, fmt.Errorf("cinc: decode data bag %q search row: %w", bag, err)
		}
		return item, nil
	})
}

// decodeRows adapts a row iterator to one of decoded values. A row error, or
// a decode error, is yielded once as the zero value and ends iteration.
func decodeRows[T any](rows iter.Seq2[json.RawMessage, error], decode func(json.RawMessage) (T, error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		for row, err := range rows {
			if err != nil {
				yield(zero, err)
				return
			}
			v, err := decode(row)
			if err != nil {
				yield(zero, err)
				return
			}
			if !yield(v, nil) {
				return
			}
		}
	}
}

// SearchAll pages through every result, returning all rows.
// WithStart(n) is respected as the absolute offset of the first row to fetch;
// subsequent pages advance the offset by the number of rows received per page.
//
// SearchAll holds the whole result set in memory; use All to process rows
// page by page instead.
func (s *SearchService) SearchAll(ctx context.Context, index, query string, opts ...SearchOption) ([]json.RawMessage, error) {
	var all []json.RawMessage
	err := s.eachPage(ctx, index, query, opts, func(res *SearchResult, offset int) bool {
		if all == nil {
			// Preallocate from the server-reported total so paging through a
			// large result set doesn't repeatedly regrow and copy the slice.
			// Cap the hint so a misreporting server can't force a huge up-front
			// allocation; append still grows past the hint if needed.
			capHint := res.Total - offset
			const maxPrealloc = 100_000
			if capHint > maxPrealloc {
				capHint = maxPrealloc
			}
			if capHint > 0 {
				all = make([]json.RawMessage, 0, capHint)
			}
		}
		all = append(all, res.Rows...)
		return true
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// eachPage is the single paging loop behind All and SearchAll. It requests
// successive pages and calls fn with each one and the offset it was
// requested at; fn returns false to stop without fetching further pages.
func (s *SearchService) eachPage(ctx context.Context, index, query string, opts []SearchOption, fn func(res *SearchResult, offset int) bool) error {
	p := searchParams{rows: 1000}
	for _, o := range opts {
		o(&p)
	}
	offset := p.start
	for {
		res, _, err := s.Query(ctx, index, query,
			WithStart(offset), WithRows(p.rows), WithPartial(p.partial))
		if err != nil {
			return err
		}
		if !fn(res, offset) {
			return nil
		}
		offset += len(res.Rows)
		if len(res.Rows) == 0 || offset >= res.Total {
			return nil
		}
	}
}
