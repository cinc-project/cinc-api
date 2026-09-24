// search_test.go
package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
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

// pagedSearchServer serves totalRows rows ({"idx":N}) in pages honouring the
// start/rows query parameters, and reports the start of every request.
func pagedSearchServer(t *testing.T, totalRows int, onRequest func(start int)) *Client {
	t.Helper()
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var start, rows int
		fmt.Sscanf(r.URL.Query().Get("start"), "%d", &start)
		fmt.Sscanf(r.URL.Query().Get("rows"), "%d", &rows)
		onRequest(start)
		out := []json.RawMessage{}
		for i := start; i < start+rows && i < totalRows; i++ {
			out = append(out, json.RawMessage(fmt.Sprintf(`{"idx":%d}`, i)))
		}
		json.NewEncoder(w).Encode(SearchResult{Total: totalRows, Start: start, Rows: out})
	})
	return newTestClient(t, srv.Server)
}

func TestSearch_AllIter(t *testing.T) {
	var starts []int
	c := pagedSearchServer(t, 7, func(s int) { starts = append(starts, s) })

	var got []int
	for row, err := range c.Search.All(context.Background(), "node", "*:*",
		WithStart(1), WithRows(3)) {
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		var v struct{ Idx int }
		if err := json.Unmarshal(row, &v); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got = append(got, v.Idx)
	}
	if fmt.Sprint(got) != "[1 2 3 4 5 6]" {
		t.Errorf("rows = %v, want [1 2 3 4 5 6]", got)
	}
	if fmt.Sprint(starts) != "[1 4]" {
		t.Errorf("page starts = %v, want [1 4]", starts)
	}
}

// TestSearch_AllIter_BreakStopsPaging verifies that breaking out of the loop
// stops further page requests: only the page holding the row we stopped on
// is fetched.
func TestSearch_AllIter_BreakStopsPaging(t *testing.T) {
	requests := 0
	c := pagedSearchServer(t, 100, func(int) { requests++ })

	seen := 0
	for _, err := range c.Search.All(context.Background(), "node", "*:*", WithRows(10)) {
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		seen++
		if seen == 12 { // second row of the second page
			break
		}
	}
	if requests != 2 {
		t.Errorf("page requests = %d, want 2 (no fetch after break)", requests)
	}
}

func TestSearch_AllIter_Error(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node",
		cinctest.Route{Status: 404, Body: `{"error":["no index"]}`})
	c := newTestClient(t, srv.Server)

	n := 0
	for row, err := range c.Search.All(context.Background(), "node", "*:*") {
		n++
		if err == nil || row != nil {
			t.Fatalf("got row %s, err %v; want nil row and an error", row, err)
		}
	}
	if n != 1 {
		t.Errorf("iterations = %d, want exactly 1 (the error)", n)
	}
}

// TestSearch_AllIter_ContextCancel verifies that cancelling ctx mid-iteration
// stops paging and surfaces the context error.
func TestSearch_AllIter_ContextCancel(t *testing.T) {
	requests := 0
	c := pagedSearchServer(t, 100, func(int) { requests++ })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var lastErr error
	rows := 0
	for _, err := range c.Search.All(ctx, "node", "*:*", WithRows(10)) {
		if err != nil {
			lastErr = err
			continue
		}
		rows++
		if rows == 10 {
			cancel() // before the second page is requested
		}
	}
	if !errors.Is(lastErr, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", lastErr)
	}
	if requests != 1 || rows != 10 {
		t.Errorf("requests = %d, rows = %d; want 1 and 10", requests, rows)
	}
}

func TestSearch_AllIter_Partial(t *testing.T) {
	var methods []string
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ops-Authorization-1") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		methods = append(methods, r.Method)
		w.Write([]byte(`{"total":1,"start":0,"rows":[{"data":{"ip":"10.0.0.1"}}]}`))
	})
	c := newTestClient(t, srv.Server)
	n := 0
	for _, err := range c.Search.All(context.Background(), "node", "*:*",
		WithPartial(map[string][]string{"ip": {"ipaddress"}})) {
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		n++
	}
	if n != 1 || fmt.Sprint(methods) != "[POST]" {
		t.Errorf("rows = %d, methods = %v; want 1 row via [POST]", n, methods)
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

// Search row fixtures in the shapes erchef (chef_wm_search.erl) and
// cinc-server-ng (internal/api/search.go) put on the wire.
const (
	// A partial search (POST) row: exactly url and data.
	partialNodeRow = `{"url":"https://chef.example/organizations/o/nodes/web01","data":{"name":"web01","kernel.release":"6.1.0"}}`
	// A full search of a data bag index: chef_data_bag_item:wrap_item/3.
	wrappedItemRow = `{"name":"data_bag_item_users_alice","json_class":"Chef::DataBagItem","chef_type":"data_bag_item","data_bag":"users","raw_data":{"id":"alice","shell":"/bin/zsh"}}`
	// A full node search row: the stored node, unchanged.
	fullNodeRow = `{"name":"web01","chef_type":"node","json_class":"Chef::Node","chef_environment":"prod","run_list":["recipe[base]"],"normal":{"tier":"web"},"default":{},"override":{},"automatic":{"platform":"ubuntu"}}`
)

func TestUnwrapSearchRow(t *testing.T) {
	tests := []struct {
		name string
		row  string
		want string
	}{
		{"partial row yields data", partialNodeRow, `{"name":"web01","kernel.release":"6.1.0"}`},
		{"partial data bag row yields data", `{"url":"https://h/organizations/o/data/users/alice","data":{"shell":"/bin/zsh"}}`, `{"shell":"/bin/zsh"}`},
		{"wrapped data bag item yields raw_data", wrappedItemRow, `{"id":"alice","shell":"/bin/zsh"}`},
		{"full node unchanged", fullNodeRow, fullNodeRow},
		// Objects that merely carry an envelope's keys are not envelopes.
		{"item with url and data plus id unchanged", `{"id":"x","url":"u","data":{"a":1}}`, `{"id":"x","url":"u","data":{"a":1}}`},
		{"data without url unchanged", `{"name":"n","data":{"a":1}}`, `{"name":"n","data":{"a":1}}`},
		{"partial with non-string url unchanged", `{"url":1,"data":{}}`, `{"url":1,"data":{}}`},
		{"partial with non-object data unchanged", `{"url":"u","data":"x"}`, `{"url":"u","data":"x"}`},
		{"partial with null data unchanged", `{"url":"u","data":null}`, `{"url":"u","data":null}`},
		{"raw_data without class unchanged", `{"id":"x","raw_data":{"a":1}}`, `{"id":"x","raw_data":{"a":1}}`},
		{"class without chef_type unchanged", `{"json_class":"Chef::DataBagItem","raw_data":{"id":"x"}}`, `{"json_class":"Chef::DataBagItem","raw_data":{"id":"x"}}`},
		{"chef_type without class unchanged", `{"chef_type":"data_bag_item","raw_data":{"id":"x"}}`, `{"chef_type":"data_bag_item","raw_data":{"id":"x"}}`},
		{"envelope with non-object raw_data unchanged", `{"json_class":"Chef::DataBagItem","chef_type":"data_bag_item","raw_data":[1]}`, `{"json_class":"Chef::DataBagItem","chef_type":"data_bag_item","raw_data":[1]}`},
		{"array unchanged", `[1,2]`, `[1,2]`},
		{"null unchanged", `null`, `null`},
		{"invalid JSON unchanged", `{"url":`, `{"url":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnwrapSearchRow(json.RawMessage(tt.row))
			if string(got) != tt.want {
				t.Errorf("UnwrapSearchRow(%s) = %s, want %s", tt.row, got, tt.want)
			}
		})
	}
}

func TestWithPartialPaths(t *testing.T) {
	var p searchParams
	WithPartialPaths("name", "kernel.release", "", "a.b.c")(&p)
	want := map[string][]string{
		"name":           {"name"},
		"kernel.release": {"kernel", "release"},
		"a.b.c":          {"a", "b", "c"},
	}
	if !reflect.DeepEqual(p.partial, want) {
		t.Fatalf("partial = %v, want %v", p.partial, want)
	}
}

// TestWithPartialPaths_ComposesWithPartial checks that projection options
// accumulate in order, a later one winning on a shared key, and that
// WithPartial never writes to the caller's map.
func TestWithPartialPaths_ComposesWithPartial(t *testing.T) {
	keys := map[string][]string{"n": {"name"}, "platform": {"automatic", "platform"}}
	var p searchParams
	for _, o := range []SearchOption{
		WithPartial(keys),
		WithPartialPaths("platform", "tier"),
		WithPartial(map[string][]string{"tier": {"normal", "tier"}}),
	} {
		o(&p)
	}
	want := map[string][]string{
		"n":        {"name"},
		"platform": {"platform"},
		"tier":     {"normal", "tier"},
	}
	if !reflect.DeepEqual(p.partial, want) {
		t.Fatalf("partial = %v, want %v", p.partial, want)
	}
	if len(keys) != 2 || !reflect.DeepEqual(keys["platform"], []string{"automatic", "platform"}) {
		t.Fatalf("WithPartial modified the caller's map: %v", keys)
	}
}

func TestSearch_QueryWithPartialPathsPosts(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/search/node", cinctest.Route{
		Body: `{"total":1,"start":0,"rows":[` + partialNodeRow + `]}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string][]string
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode body %s: %v", body, err)
			}
			want := map[string][]string{"kernel.release": {"kernel", "release"}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %v, want %v", got, want)
			}
		}})
	c := newTestClient(t, srv.Server)
	res, _, err := c.Search.Query(context.Background(), "node", "*:*", WithPartialPaths("kernel.release"))
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("Query: %+v %v", res, err)
	}
}

func TestSearch_Nodes(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node", cinctest.Route{
		Body: `{"total":2,"start":0,"rows":[` + fullNodeRow + `,{"name":"db01","run_list":[]}]}`,
		Assert: func(t *testing.T, r *http.Request, _ []byte) {
			if q := r.URL.Query().Get("q"); q != "role:web" {
				t.Errorf("q = %q", q)
			}
		}})
	c := newTestClient(t, srv.Server)
	var got []*Node
	for n, err := range c.Search.Nodes(context.Background(), "role:web") {
		if err != nil {
			t.Fatalf("Nodes: %v", err)
		}
		got = append(got, n)
	}
	if len(got) != 2 || got[0].Name != "web01" || got[1].Name != "db01" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Environment != "prod" || got[0].Automatic["platform"] != "ubuntu" || got[0].Normal["tier"] != "web" {
		t.Errorf("node not fully decoded: %+v", got[0])
	}
}

func TestSearch_NodesRejectsPartial(t *testing.T) {
	srv := cinctest.New(t) // no routes: any request fails the test
	c := newTestClient(t, srv.Server)
	for _, opt := range []SearchOption{
		WithPartial(map[string][]string{"n": {"name"}}),
		WithPartialPaths("name"),
	} {
		n := 0
		for node, err := range c.Search.Nodes(context.Background(), "*:*", opt) {
			n++
			if node != nil || err == nil || !strings.Contains(err.Error(), "partial") {
				t.Errorf("got (%v, %v), want a nil node and an error naming partial search", node, err)
			}
		}
		if n != 1 {
			t.Errorf("yielded %d times, want once", n)
		}
	}
}

func TestSearch_NodesYieldsPageError(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node", cinctest.Route{Status: http.StatusForbidden, Body: `{"error":["no"]}`})
	c := newTestClient(t, srv.Server)
	n := 0
	for node, err := range c.Search.Nodes(context.Background(), "*:*") {
		n++
		if node != nil || err == nil {
			t.Errorf("got (%v, %v), want an error", node, err)
		}
	}
	if n != 1 {
		t.Errorf("yielded %d times, want once", n)
	}
}

func TestSearch_NodesDecodeError(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node", cinctest.Route{
		Body: `{"total":2,"start":0,"rows":[{"name":7},{"name":"never"}]}`})
	c := newTestClient(t, srv.Server)
	n := 0
	for node, err := range c.Search.Nodes(context.Background(), "*:*") {
		n++
		if node != nil || err == nil || !strings.Contains(err.Error(), "node") {
			t.Errorf("got (%v, %v), want a decode error", node, err)
		}
	}
	if n != 1 {
		t.Errorf("yielded %d times, want once (iteration ends on a bad row)", n)
	}
}

func TestSearch_NodesBreakStops(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/node", cinctest.Route{
		Body: `{"total":2,"start":0,"rows":[` + fullNodeRow + `,{"name":"db01"}]}`})
	c := newTestClient(t, srv.Server)
	n := 0
	for range c.Search.Nodes(context.Background(), "*:*") {
		n++
		break
	}
	if n != 1 {
		t.Errorf("yielded %d times after break, want 1", n)
	}
}

func TestSearch_DataBagItems(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/users", cinctest.Route{
		Body: `{"total":2,"start":0,"rows":[` + wrappedItemRow +
			`,{"name":"data_bag_item_users_bob","json_class":"Chef::DataBagItem","chef_type":"data_bag_item","data_bag":"users","raw_data":{"id":"bob","groups":["ops"]}}]}`,
		Assert: func(t *testing.T, r *http.Request, _ []byte) {
			if q := r.URL.Query().Get("q"); q != "shell:*" {
				t.Errorf("q = %q", q)
			}
		}})
	c := newTestClient(t, srv.Server)
	var got []DataBagItem
	for item, err := range c.Search.DataBagItems(context.Background(), "users", "shell:*") {
		if err != nil {
			t.Fatalf("DataBagItems: %v", err)
		}
		got = append(got, item)
	}
	want := []DataBagItem{
		{"id": "alice", "shell": "/bin/zsh"},
		{"id": "bob", "groups": []any{"ops"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSearch_DataBagItemsPartial(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/search/users", cinctest.Route{
		Body: `{"total":1,"start":0,"rows":[{"url":"https://h/organizations/o/data/users/alice","data":{"id":"alice","shell":"/bin/zsh"}}]}`})
	c := newTestClient(t, srv.Server)
	var got []DataBagItem
	for item, err := range c.Search.DataBagItems(context.Background(), "users", "*:*", WithPartialPaths("id", "shell")) {
		if err != nil {
			t.Fatalf("DataBagItems: %v", err)
		}
		got = append(got, item)
	}
	if len(got) != 1 || got[0].ID() != "alice" || got[0]["shell"] != "/bin/zsh" {
		t.Fatalf("got %v", got)
	}
}

func TestSearch_DataBagItemsDecodeError(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/search/users", cinctest.Route{
		Body: `{"total":1,"start":0,"rows":[[1]]}`})
	c := newTestClient(t, srv.Server)
	for item, err := range c.Search.DataBagItems(context.Background(), "users", "*:*") {
		if item != nil || err == nil || !strings.Contains(err.Error(), "users") {
			t.Errorf("got (%v, %v), want a decode error naming the bag", item, err)
		}
	}
}
