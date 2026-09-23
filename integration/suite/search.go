package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
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
