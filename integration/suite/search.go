package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

func testSearchQuery(t *testing.T, c *cinc.Client) {
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
