package suite

import (
	"context"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testClientLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "client")
	cleanup(t, "client "+name, func(ctx context.Context) error {
		_, err := c.Clients.Delete(ctx, name)
		return err
	})

	created, _, err := c.Clients.Create(ctx, &cinc.APIClient{Name: name})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ChefKey.PrivateKey == "" {
		t.Fatalf("Create returned no private key: %+v", created)
	}

	if _, _, err := c.Clients.Update(ctx, &cinc.APIClient{Name: name, Validator: true}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got, _, err := c.Clients.Get(ctx, name); err != nil || !got.Validator {
		t.Fatalf("Get after update: %+v %v", got, err)
	}

	key, _, err := c.Clients.Reregister(ctx, name)
	if err != nil {
		t.Fatalf("Reregister: %v", err)
	}
	if key.PrivateKey == "" || key.PrivateKey == created.ChefKey.PrivateKey {
		t.Fatal("Reregister did not return a fresh private key")
	}
}
