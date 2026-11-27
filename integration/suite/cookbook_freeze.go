package suite

import (
	"context"
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testCookbookFreeze uploads a version frozen, checks that a plain re-upload
// is refused as ErrCookbookFrozen, and that WithForce replaces it while the
// version stays frozen (erchef never unfreezes one).
func testCookbookFreeze(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "cookbook")
	cleanup(t, "cookbook "+name+" 1.0.0", func(ctx context.Context) error {
		_, err := c.Cookbooks.Delete(ctx, name, "1.0.0")
		return err
	})
	cb := localCookbook(t, name, "1.0.0", nil)
	if err := c.Cookbooks.Upload(ctx, cb, cinc.WithFreeze()); err != nil {
		t.Fatalf("Upload WithFreeze: %v", err)
	}
	frozen := func(when string) {
		t.Helper()
		got, _, err := c.Cookbooks.Get(ctx, name, "1.0.0")
		if err != nil {
			t.Fatalf("Get %s: %v", when, err)
		}
		if !got.Frozen {
			t.Fatalf("Frozen = false %s, want true", when)
		}
	}
	frozen("after a frozen upload")

	err := c.Cookbooks.Upload(ctx, cb)
	if !errors.Is(err, cinc.ErrCookbookFrozen) || !errors.Is(err, cinc.ErrConflict) {
		t.Fatalf("re-upload of a frozen version: err = %v, want ErrCookbookFrozen", err)
	}
	if err := c.Cookbooks.Upload(ctx, cb, cinc.WithForce()); err != nil {
		t.Fatalf("Upload WithForce: %v", err)
	}
	frozen("after a forced upload")
}
