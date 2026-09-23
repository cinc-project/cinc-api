package suite

import (
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testRequiredRecipe checks the default: a server with no required recipe
// configured answers 404.
func testRequiredRecipe(t *testing.T, _ Target, c *cinc.Client) {
	if _, _, err := c.RequiredRecipe.Get(t.Context()); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("RequiredRecipe.Get: err = %v, want ErrNotFound (no required recipe configured)", err)
	}
}

func testLicense(t *testing.T, _ Target, c *cinc.Client) {
	lic, _, err := c.License.Get(t.Context())
	if err != nil {
		t.Fatalf("License.Get: %v", err)
	}
	if lic.NodeCount < 0 || lic.NodeLicense < 0 {
		t.Fatalf("license = %+v", lic)
	}
}

func testStats(t *testing.T, tgt Target, c *cinc.Client) {
	if tgt.StatsUser == "" {
		t.Fatalf("target %s has no stats credentials", tgt.Name)
	}
	stats, _, err := c.Stats.Get(t.Context(), tgt.StatsUser, tgt.StatsPassword)
	if err != nil {
		t.Fatalf("Stats.Get: %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("Stats.Get returned no metric families")
	}
}
