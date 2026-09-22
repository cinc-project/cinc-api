package cinc

import (
	"context"
	"encoding/json"
	"iter"
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
