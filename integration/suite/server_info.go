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

// testServerAPIVersion reads the API version negotiation both ways: from the
// /server_api_version probe, and from the header on an ordinary response.
// The client asks for version 1, so the server must support it and answer
// with it.
func testServerAPIVersion(t *testing.T, _ Target, c *cinc.Client) {
	v, _, err := c.ServerAPIVersion(t.Context())
	if err != nil {
		t.Fatalf("ServerAPIVersion: %v", err)
	}
	if v.Min > 1 || v.Max < 1 || v.Request != 1 || v.Response != 1 {
		t.Fatalf("ServerAPIVersion = %+v, want a range including 1 and 1 negotiated", v)
	}

	_, resp, err := c.Nodes.List(t.Context())
	if err != nil {
		t.Fatalf("Nodes.List: %v", err)
	}
	h, ok := resp.ServerAPIVersion()
	if !ok {
		t.Fatalf("Nodes.List response has no usable X-Ops-Server-API-Version header: %q",
			resp.HTTPResponse.Header.Get("X-Ops-Server-API-Version"))
	}
	if h != *v {
		t.Fatalf("header version %+v differs from the probe's %+v", h, *v)
	}
}
