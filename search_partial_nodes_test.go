package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func collectPartial(t *testing.T, seq func(func(*PartialNode, error) bool)) ([]*PartialNode, error) {
	t.Helper()
	var out []*PartialNode
	for n, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, n)
	}
	return out, nil
}

func TestSearch_PartialNodesSendsProjectionAndDecodesRows(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/search/node", cinctest.Route{
		Body: `{"total":2,"start":0,"rows":[
			{"url":"https://chef/organizations/o/nodes/web01","data":{"ohai_time":1700000000.5,"platform":"ubuntu","ipaddress":["10.0.0.1"],"big":9007199254740993}},
			{"url":"https://chef/organizations/o/nodes/db%2E01","data":{"ohai_time":null,"platform":null}}
		]}`,
		Assert: func(t *testing.T, r *http.Request, body []byte) {
			if q := r.URL.Query().Get("q"); q != "role:web" {
				t.Errorf("q = %q", q)
			}
			var proj map[string][]string
			if err := json.Unmarshal(body, &proj); err != nil {
				t.Fatalf("projection body %s: %v", body, err)
			}
			if strings.Join(proj["ohai_time"], ".") != "ohai_time" || strings.Join(proj["cloud.provider"], ".") != "cloud.provider" {
				t.Errorf("projection = %v", proj)
			}
		}})
	c := newTestClient(t, srv.Server)

	got, err := collectPartial(t, c.Search.PartialNodes(context.Background(), "role:web", []string{"ohai_time", "cloud.provider"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "web01" || got[1].Name != "db.01" {
		t.Fatalf("names = %+v", got)
	}
	web := got[0]
	if web.URL != "https://chef/organizations/o/nodes/web01" {
		t.Errorf("URL = %q", web.URL)
	}
	if s, ok := web.Scalar("platform"); !ok || s != "ubuntu" {
		t.Errorf("Scalar(platform) = %q, %v", s, ok)
	}
	if s, ok := web.Scalar("ipaddress"); !ok || s != "10.0.0.1" {
		t.Errorf("Scalar(ipaddress) = %q, %v (first element of an array)", s, ok)
	}
	if s, _ := web.Scalar("big"); s != "9007199254740993" {
		t.Errorf("Scalar(big) = %q, want the exact integer", s)
	}
	if v, ok := web.Value("ohai_time"); !ok || v == nil {
		t.Errorf("Value(ohai_time) = %v, %v", v, ok)
	}
	if _, ok := web.Value("missing"); ok {
		t.Error("Value(missing) found")
	}
	want := time.Unix(1700000000, 500_000_000)
	if at, ok := web.LastCheckin(); !ok || !at.Equal(want) {
		t.Errorf("LastCheckin = %v, %v; want %v", at, ok, want)
	}
	// A projected attribute the node does not have comes back as null.
	if _, ok := got[1].Scalar("platform"); ok {
		t.Error("Scalar of a null projection reported ok")
	}
	if _, ok := got[1].LastCheckin(); ok {
		t.Error("LastCheckin of a node without ohai_time reported ok")
	}
}

func TestSearch_PartialNodesNeedsAPath(t *testing.T) {
	srv := cinctest.New(t) // no routes: any request fails the test
	c := newTestClient(t, srv.Server)
	n := 0
	for node, err := range c.Search.PartialNodes(context.Background(), "*:*", nil) {
		n++
		if node != nil || err == nil || !strings.Contains(err.Error(), "path") {
			t.Errorf("got (%v, %v), want an error asking for a path", node, err)
		}
	}
	if n != 1 {
		t.Errorf("yielded %d times, want once", n)
	}
}

func TestSearch_PartialNodesRejectsRowThatIsNotAProjection(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/search/node", cinctest.Route{
		Body: `{"total":1,"start":0,"rows":[` + fullNodeRow + `]}`,
	})
	c := newTestClient(t, srv.Server)
	_, err := collectPartial(t, c.Search.PartialNodes(context.Background(), "*:*", []string{"platform"}))
	if err == nil || !strings.Contains(err.Error(), "partial") {
		t.Fatalf("err = %v, want an error about a non-partial row", err)
	}
}

// Name falls back to a projected "name" when the row's URL does not end in
// a node name.
func TestSearch_PartialNodesNameFromProjection(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/search/node", cinctest.Route{
		Body: `{"total":2,"start":0,"rows":[{"url":"","data":{"name":"web01"}},{"url":"%zz","data":{"name":"web02"}}]}`,
	})
	c := newTestClient(t, srv.Server)
	got, err := collectPartial(t, c.Search.PartialNodes(context.Background(), "*:*", []string{"name"}))
	if err != nil || len(got) != 2 || got[0].Name != "web01" || got[1].Name != "web02" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, ok := got[0].Scalar("platform"); ok {
		t.Error("Scalar of a path that was not projected reported ok")
	}
}
