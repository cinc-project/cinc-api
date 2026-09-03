// search_test.go
package cinc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestSearch_Query(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node",
		cinctest.Route{
			Body: `{"total":1,"start":0,"rows":[{"name":"web01"}]}`,
			Assert: func(t *testing.T, r *http.Request, _ []byte) {
				q := r.URL.Query()
				if q.Get("q") != "chef_environment:prod" {
					t.Errorf("q = %q", q.Get("q"))
				}
				if q.Get("rows") != "5" {
					t.Errorf("rows = %q", q.Get("rows"))
				}
			}})
	c := newTestClient(t, srv.Server)

	res, _, err := c.Search.Query(context.Background(), "node",
		"chef_environment:prod", WithRows(5))
	if err != nil || res.Total != 1 || len(res.Rows) != 1 {
		t.Fatalf("Query: %+v %v", res, err)
	}
	var n Node
	if err := json.Unmarshal(res.Rows[0], &n); err != nil || n.Name != "web01" {
		t.Fatalf("decode row: %+v %v", n, err)
	}
}

// TestSearch_AllWithStart verifies that SearchAll with WithStart(10) sends
// correct absolute offsets on successive pages: page 1 start=10,
// page 2 start=10+pageSize, etc., and that all rows are returned exactly once.
func TestSearch_AllWithStart(t *testing.T) {
	// Total data: offsets 0-19 (20 rows). WithStart(10) should fetch rows 10-19.
	// Page size = 5, so two pages: start=10 (5 rows), start=15 (5 rows).
	const pageSize = 5
	const startOffset = 10
	const totalRows = 20

	var requestStarts []int
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Require signed requests (cinctest.dispatch normally checks this).
		if r.Header.Get("X-Ops-Authorization-1") == "" {
			fmt.Fprint(w, `{}`)
			return
		}
		q := r.URL.Query()
		start := 0
		fmt.Sscanf(q.Get("start"), "%d", &start)
		requestStarts = append(requestStarts, start)

		// Build a page of rows beginning at 'start'.
		end := start + pageSize
		if end > totalRows {
			end = totalRows
		}
		var rows []string
		for i := start; i < end; i++ {
			rows = append(rows, fmt.Sprintf(`{"idx":%d}`, i))
		}
		rowsJSON := "["
		for i, r := range rows {
			if i > 0 {
				rowsJSON += ","
			}
			rowsJSON += r
		}
		rowsJSON += "]"
		fmt.Fprintf(w, `{"total":%d,"start":%d,"rows":%s}`, totalRows, start, rowsJSON)
	})
	c := newTestClient(t, srv.Server)

	rows, err := c.Search.SearchAll(context.Background(), "node", "*:*",
		WithStart(startOffset), WithRows(pageSize))
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}

	// Expect exactly 2 requests.
	if len(requestStarts) != 2 {
		t.Fatalf("expected 2 page requests, got %d (starts=%v)", len(requestStarts), requestStarts)
	}
	if requestStarts[0] != 10 {
		t.Errorf("page 1 start = %d, want 10", requestStarts[0])
	}
	if requestStarts[1] != 15 {
		t.Errorf("page 2 start = %d, want 15 (10 + 5), got %d", requestStarts[1], requestStarts[1])
	}

	// Expect 10 rows returned (rows 10-19).
	if len(rows) != 10 {
		t.Fatalf("got %d rows, want 10", len(rows))
	}
	// Verify first row is idx=10, last is idx=19.
	var first, last struct{ Idx int }
	json.Unmarshal(rows[0], &first)
	json.Unmarshal(rows[9], &last)
	if first.Idx != 10 {
		t.Errorf("first row idx = %d, want 10", first.Idx)
	}
	if last.Idx != 19 {
		t.Errorf("last row idx = %d, want 19", last.Idx)
	}
}

// TestSearch_All_CapsPrealloc drives the preallocation cap: a server reporting
// an enormous total must not trigger a huge up-front allocation, and all rows
// are still returned correctly.
func TestSearch_All_CapsPrealloc(t *testing.T) {
	srv := cinctest.New(t)
	page := 0
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if page == 0 {
			page++
			// Total far exceeds maxPrealloc (100_000); only one row is actually
			// returned, and the next page is empty to terminate paging.
			w.Write([]byte(`{"total":5000000,"start":0,"rows":[{"name":"a"}]}`))
			return
		}
		w.Write([]byte(`{"total":5000000,"start":1,"rows":[]}`))
	})
	c := newTestClient(t, srv.Server)
	rows, err := c.Search.SearchAll(context.Background(), "node", "*:*")
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if cap(rows) > 100_000 {
		t.Fatalf("preallocated cap = %d, want it capped at 100_000", cap(rows))
	}
}

func TestSearch_PartialUsesPOST(t *testing.T) {
	// WithPartial switches the request to POST with a body containing the
	// requested key projection.
	var (
		gotMethod string
		bodyMap   map[string][]string
	)
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&bodyMap)
		w.Write([]byte(`{"total":0,"start":0,"rows":[]}`))
	})
	c := newTestClient(t, srv.Server)
	keys := map[string][]string{"ip": {"ipaddress"}, "name": {"name"}}
	_, _, err := c.Search.Query(context.Background(), "node", "*:*", WithPartial(keys))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST when WithPartial is set", gotMethod)
	}
	if len(bodyMap) != 2 || bodyMap["ip"][0] != "ipaddress" {
		t.Errorf("body = %+v, want partial key projection", bodyMap)
	}
}

func TestSearch_Query_Error(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node",
		cinctest.Route{Status: 500, Body: `{"error":["boom"]}`})
	c := newTestClient(t, srv.Server)
	res, _, err := c.Search.Query(context.Background(), "node", "*:*")
	if err == nil {
		t.Fatal("expected error from 500")
	}
	if res != nil {
		t.Errorf("res = %+v, want nil on error", res)
	}
}

func TestSearch_All_PropagatesError(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node",
		cinctest.Route{Status: 404, Body: `{"error":["no index"]}`})
	c := newTestClient(t, srv.Server)
	rows, err := c.Search.SearchAll(context.Background(), "node", "*:*")
	if err == nil {
		t.Fatal("expected error from underlying Query")
	}
	if rows != nil {
		t.Errorf("rows = %+v, want nil on error", rows)
	}
}

func TestSearch_All(t *testing.T) {
	srv := cinctest.New(t)
	page := 0
	srv.Handle("GET /organizations/o/search/node", cinctest.Route{}) // unused
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if page == 0 {
			page++
			w.Write([]byte(`{"total":3,"start":0,"rows":[{"name":"a"},{"name":"b"}]}`))
			return
		}
		w.Write([]byte(`{"total":3,"start":2,"rows":[{"name":"c"}]}`))
	})
	c := newTestClient(t, srv.Server)
	rows, err := c.Search.SearchAll(context.Background(), "node", "*:*", WithRows(2))
	if err != nil || len(rows) != 3 {
		t.Fatalf("SearchAll: %d rows, %v", len(rows), err)
	}
}

func TestSearch_Indexes(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search", cinctest.Route{
		Body: `{
			"node":"https://x/organizations/o/search/node",
			"role":"https://x/organizations/o/search/role",
			"client":"https://x/organizations/o/search/client"
		}`,
	})
	c := newTestClient(t, srv.Server)

	idx, _, err := c.Search.Indexes(context.Background())
	if err != nil {
		t.Fatalf("Indexes: %v", err)
	}
	if idx["node"] == "" || idx["role"] == "" || idx["client"] == "" {
		t.Fatalf("Indexes = %v", idx)
	}
}
