package suite

import (
	"context"
	"errors"
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
	if created.ChefKey.PrivateKey == "" || created.URI == "" || created.ChefKey.Name != "default" {
		t.Fatalf("Create = %+v, want a uri and a generated default key", created)
	}

	if _, _, err := c.Clients.Update(ctx, name, &cinc.APIClient{Name: name, Validator: true}); err != nil {
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

// testClientRename renames a client through Update: the body carries the new
// name, the path the old one. The client keeps its key under the new name.
func testClientRename(t *testing.T, tgt Target, c *cinc.Client) {
	ctx := t.Context()
	oldName, newName := uniqueName(t, "client"), uniqueName(t, "client")
	for _, n := range []string{oldName, newName} {
		cleanup(t, "client "+n, func(ctx context.Context) error {
			_, err := c.Clients.Delete(ctx, n)
			return err
		})
	}
	created, _, err := c.Clients.Create(ctx, &cinc.APIClient{Name: oldName})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, _, err := c.Clients.Update(ctx, oldName, &cinc.APIClient{Name: newName})
	if err != nil {
		t.Fatalf("Update to rename: %v", err)
	}
	if updated.Name != newName {
		t.Fatalf("Update = %+v, want name %s", updated, newName)
	}
	if _, _, err := c.Clients.Get(ctx, oldName); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get old name after rename: err = %v, want ErrNotFound", err)
	}
	if got, _, err := c.Clients.Get(ctx, newName); err != nil || got.Name != newName {
		t.Fatalf("Get new name after rename: %+v %v", got, err)
	}
	as := clientAs(t, tgt, newName, created.ChefKey.PrivateKey)
	if _, _, err := as.Nodes.List(ctx); err != nil {
		t.Fatalf("list nodes as the renamed client with its original key: %v", err)
	}
}

// testClientReregisterKeepsClientUsable regenerates a client's default key and
// signs a request with the new one; the old one no longer authenticates.
func testClientReregisterKeepsClientUsable(t *testing.T, tgt Target, c *cinc.Client) {
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

	key, _, err := c.Clients.Reregister(ctx, name)
	if err != nil {
		t.Fatalf("Reregister: %v", err)
	}
	if key.PrivateKey == "" || key.PrivateKey == created.ChefKey.PrivateKey {
		t.Fatalf("Reregister = %+v, want a fresh private key", key)
	}
	if _, _, err := clientAs(t, tgt, name, key.PrivateKey).Nodes.List(ctx); err != nil {
		t.Fatalf("list nodes with the regenerated key: %v", err)
	}
	if _, _, err := clientAs(t, tgt, name, created.ChefKey.PrivateKey).Nodes.List(ctx); !errors.Is(err, cinc.ErrUnauthorized) {
		t.Fatalf("list nodes with the old key: err = %v, want ErrUnauthorized", err)
	}
	keys, _, err := c.Keys.Client(name).List(ctx)
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "default" {
		t.Fatalf("keys = %+v, want just default", keys)
	}
}

// testClientReregisterKeyless reregisters a client created without a key,
// which gets a default key created.
func testClientReregisterKeyless(t *testing.T, tgt Target, c *cinc.Client) {
	ctx := t.Context()
	name := newClient(t, c)
	if _, err := c.Keys.Client(name).Delete(ctx, "default"); err != nil {
		t.Fatalf("delete default key: %v", err)
	}
	key, _, err := c.Clients.Reregister(ctx, name)
	if err != nil {
		t.Fatalf("Reregister: %v", err)
	}
	if _, _, err := clientAs(t, tgt, name, key.PrivateKey).Nodes.List(ctx); err != nil {
		t.Fatalf("list nodes with the created key: %v", err)
	}
}
