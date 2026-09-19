package cinc

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
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

// WithPartial requests partial search projection (key -> attribute path).
func WithPartial(keys map[string][]string) SearchOption {
	return func(p *searchParams) { p.partial = keys }
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

// SearchAll pages through every result, returning all rows.
// WithStart(n) is respected as the absolute offset of the first row to fetch;
// subsequent pages advance the offset by the number of rows received per page.
func (s *SearchService) SearchAll(ctx context.Context, index, query string, opts ...SearchOption) ([]json.RawMessage, error) {
	p := searchParams{rows: 1000}
	for _, o := range opts {
		o(&p)
	}
	var all []json.RawMessage
	offset := p.start
	for {
		res, _, err := s.Query(ctx, index, query,
			WithStart(offset), WithRows(p.rows), WithPartial(p.partial))
		if err != nil {
			return nil, err
		}
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
		offset += len(res.Rows)
		if len(res.Rows) == 0 || offset >= res.Total {
			return all, nil
		}
	}
}
