package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

func testSearchQuery(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	want := map[string]bool{}
	for range 3 {
		name := uniqueName(t, "search")
		want[name] = true
		cleanup(t, "node "+name, func(ctx context.Context) error {
			_, err := c.Nodes.Delete(ctx, name)
			return err
		})
		if _, err := c.Nodes.Create(ctx, &cinc.Node{Name: name, RunList: []string{}}); err != nil {
			t.Fatalf("seed node %s: %v", name, err)
		}
	}

	// The server may hold other nodes (other parallel tests, or a kept
	// server's earlier runs), so check that ours are among the results
	// rather than counting them.
	eventually(t, 30*time.Second, func() error {
		rows, err := c.Search.SearchAll(ctx, "node", "*:*")
		if err != nil {
			return err
		}
		found := 0
		for _, row := range rows {
			var n struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(row, &n); err != nil {
				return fmt.Errorf("decode search row: %w", err)
			}
			if want[n.Name] {
				found++
			}
		}
		if found != len(want) {
			return fmt.Errorf("search found %d of the %d seeded nodes", found, len(want))
		}
		return nil
	})
}

// testSearchPartial projects two fields from a node found by a unique
// attribute value; the rows come back as {"url": ..., "data": {...}}.
func testSearchPartial(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	marker := randomHex(t, 8)
	name := newSearchNode(t, c, marker, "web")

	eventually(t, 30*time.Second, func() error {
		res, _, err := c.Search.Query(ctx, "node", "search_marker:"+marker,
			cinc.WithPartial(map[string][]string{"n": {"name"}, "tier": {"tier"}}))
		if err != nil {
			return err
		}
		if len(res.Rows) != 1 {
			return fmt.Errorf("partial search returned %d rows, want 1", len(res.Rows))
		}
		var row struct {
			URL  string            `json:"url"`
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(res.Rows[0], &row); err != nil {
			return fmt.Errorf("decode partial row %s: %w", res.Rows[0], err)
		}
		if row.Data["n"] != name || row.Data["tier"] != "web" || !strings.HasSuffix(row.URL, "/nodes/"+name) {
			return fmt.Errorf("partial row = %+v, want n=%s tier=web and a URL ending in /nodes/%s", row, name, name)
		}
		return nil
	})
}

// testSearchAllPages seeds five nodes sharing a unique attribute value and
// reads them back through Search.All two rows at a time.
func testSearchAllPages(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	marker := randomHex(t, 8)
	want := map[string]bool{}
	for range 5 {
		want[newSearchNode(t, c, marker, "")] = true
	}

	eventually(t, 30*time.Second, func() error {
		got := map[string]bool{}
		for row, err := range c.Search.All(ctx, "node", "search_marker:"+marker, cinc.WithRows(2)) {
			if err != nil {
				return err
			}
			var n struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(row, &n); err != nil {
				return fmt.Errorf("decode search row: %w", err)
			}
			got[n.Name] = true
		}
		if !maps.Equal(got, want) {
			return fmt.Errorf("Search.All returned %d nodes %v, want the %d seeded", len(got), got, len(want))
		}
		return nil
	})
}

// newSearchNode creates a node carrying the normal attributes search_marker
// (and tier, when set) and registers its deletion.
func newSearchNode(t *testing.T, c *cinc.Client, marker, tier string) string {
	t.Helper()
	name := uniqueName(t, "node")
	cleanup(t, "node "+name, func(ctx context.Context) error {
		_, err := c.Nodes.Delete(ctx, name)
		return err
	})
	attrs := cinc.Attributes{"search_marker": marker}
	if tier != "" {
		attrs["tier"] = tier
	}
	if _, err := c.Nodes.Create(t.Context(), &cinc.Node{Name: name, RunList: []string{}, Normal: attrs}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	return name
}

// testSearchPartialPathsUnwrap projects a top-level field and a nested
// attribute through WithPartialPaths and reads the projection back with
// UnwrapSearchRow, which strips the {"url", "data"} envelope.
func testSearchPartialPathsUnwrap(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	marker := randomHex(t, 8)
	name := newNestedSearchNode(t, c, marker)

	eventually(t, 30*time.Second, func() error {
		res, _, err := c.Search.Query(ctx, "node", "search_marker:"+marker,
			cinc.WithPartialPaths("name", "app.version"))
		if err != nil {
			return err
		}
		if len(res.Rows) != 1 {
			return fmt.Errorf("partial search returned %d rows, want 1", len(res.Rows))
		}
		var got map[string]any
		if err := json.Unmarshal(cinc.UnwrapSearchRow(res.Rows[0]), &got); err != nil {
			return fmt.Errorf("decode unwrapped row %s: %w", res.Rows[0], err)
		}
		want := map[string]any{"name": name, "app.version": "1.2.3"}
		if !maps.Equal(got, want) {
			return fmt.Errorf("unwrapped row = %v (from %s), want %v", got, res.Rows[0], want)
		}
		return nil
	})
}

// testSearchNodes reads a node back through Search.Nodes, decoded whole.
func testSearchNodes(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	marker := randomHex(t, 8)
	name := newNestedSearchNode(t, c, marker)

	eventually(t, 30*time.Second, func() error {
		var got []*cinc.Node
		for n, err := range c.Search.Nodes(ctx, "search_marker:"+marker) {
			if err != nil {
				return err
			}
			got = append(got, n)
		}
		if len(got) != 1 {
			return fmt.Errorf("Search.Nodes returned %d nodes, want 1", len(got))
		}
		if got[0].Name != name {
			return fmt.Errorf("node name = %q, want %q", got[0].Name, name)
		}
		if v := got[0].AttributeString("app.version"); v != "1.2.3" {
			return fmt.Errorf("node app.version = %q, want 1.2.3 (node %+v)", v, got[0])
		}
		return nil
	})
}

// testSearchDataBagItems searches a data bag. A full search returns each item
// wrapped as a Chef::DataBagItem; Search.DataBagItems yields the items
// themselves, whole or projected.
func testSearchDataBagItems(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	bag := newDataBag(t, c)
	want := map[string]cinc.DataBagItem{}
	for i, port := range []float64{8080, 9090} {
		id := uniqueName(t, fmt.Sprintf("item%d", i))
		item := cinc.DataBagItem{"id": id, "port": port, "tags": []any{"a"}}
		if _, _, err := c.DataBags.Items(bag).Create(ctx, item); err != nil {
			t.Fatalf("create item %s: %v", id, err)
		}
		want[id] = item
	}

	eventually(t, 30*time.Second, func() error {
		// Pin the wire shape UnwrapSearchRow relies on.
		res, _, err := c.Search.Query(ctx, bag, "*:*")
		if err != nil {
			return err
		}
		if len(res.Rows) != len(want) {
			return fmt.Errorf("data bag search returned %d rows, want %d", len(res.Rows), len(want))
		}
		var env struct {
			Name      string `json:"name"`
			JSONClass string `json:"json_class"`
			ChefType  string `json:"chef_type"`
			DataBag   string `json:"data_bag"`
			RawData   struct {
				ID string `json:"id"`
			} `json:"raw_data"`
		}
		if err := json.Unmarshal(res.Rows[0], &env); err != nil {
			return fmt.Errorf("decode row %s: %w", res.Rows[0], err)
		}
		if env.JSONClass != "Chef::DataBagItem" || env.ChefType != "data_bag_item" ||
			env.DataBag != bag || env.Name != "data_bag_item_"+bag+"_"+env.RawData.ID {
			return fmt.Errorf("row %s is not a Chef::DataBagItem envelope for bag %s", res.Rows[0], bag)
		}

		got := map[string]cinc.DataBagItem{}
		for item, err := range c.Search.DataBagItems(ctx, bag, "*:*") {
			if err != nil {
				return err
			}
			got[item.ID()] = item
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("Search.DataBagItems = %v, want %v", got, want)
		}

		projected := map[string]cinc.DataBagItem{}
		for item, err := range c.Search.DataBagItems(ctx, bag, "port:9090", cinc.WithPartialPaths("id", "port")) {
			if err != nil {
				return err
			}
			projected[item.ID()] = item
		}
		if len(projected) != 1 {
			return fmt.Errorf("partial data bag search returned %v, want the one item on port 9090", projected)
		}
		for id, item := range projected {
			if !reflect.DeepEqual(item, cinc.DataBagItem{"id": id, "port": 9090.0}) || want[id]["port"] != 9090.0 {
				return fmt.Errorf("partial data bag item = %v, want id and port 9090 only", item)
			}
		}
		return nil
	})
}

// newNestedSearchNode creates a node carrying the normal attributes
// search_marker and app.version (1.2.3) and registers its deletion.
func newNestedSearchNode(t *testing.T, c *cinc.Client, marker string) string {
	t.Helper()
	name := uniqueName(t, "node")
	cleanup(t, "node "+name, func(ctx context.Context) error {
		_, err := c.Nodes.Delete(ctx, name)
		return err
	})
	attrs := cinc.Attributes{"search_marker": marker, "app": map[string]any{"version": "1.2.3"}}
	if _, err := c.Nodes.Create(t.Context(), &cinc.Node{Name: name, RunList: []string{}, Normal: attrs}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	return name
}
