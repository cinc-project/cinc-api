package suite

import (
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testStatusGet(t *testing.T, c *cinc.Client) {
	if _, _, err := c.Status.Get(t.Context()); err != nil {
		t.Fatalf("Status.Get: %v", err)
	}
}
