package suite

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testClientKeyLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	client := newClient(t, c)
	testKeyLifecycle(t, c.Keys.Client(client), "default")
}

// testUserKeyLifecycle adds, changes and removes an extra key on the admin
// user. It never touches the key the tests authenticate with.
func testUserKeyLifecycle(t *testing.T, tgt Target, c *cinc.Client) {
	testKeyLifecycle(t, c.Keys.User(tgt.Admin), "")
}

// testKeyLifecycle exercises one principal's keys: a server-generated key, a
// key from a caller-supplied public key, a read-modify-write expiration
// change, and deletion. existing, if set, names a key that must already be
// listed.
func testKeyLifecycle(t *testing.T, keys *cinc.KeyScope, existing string) {
	ctx := t.Context()
	generated := newKey(t, keys, true)
	supplied := newKey(t, keys, false)

	got, _, err := keys.Get(ctx, supplied)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PublicKey == "" || got.ExpirationDate != "infinity" {
		t.Fatalf("key = %+v", got)
	}

	list, _, err := keys.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listed := map[string]cinc.Key{}
	for _, k := range list {
		listed[k.Name] = k
	}
	for _, want := range []string{existing, generated, supplied} {
		if _, ok := listed[want]; want != "" && !ok {
			t.Fatalf("key %q missing from list %v", want, list)
		}
	}
	if listed[supplied].Expired {
		t.Fatalf("key %s listed as expired: %+v", supplied, listed[supplied])
	}

	const expires = "2099-01-01T00:00:00Z"
	got.ExpirationDate = expires
	if _, _, err := keys.Update(ctx, supplied, got); err != nil {
		t.Fatalf("Update expiration: %v", err)
	}
	again, _, err := keys.Get(ctx, supplied)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if again.ExpirationDate != expires || again.PublicKey != got.PublicKey {
		t.Fatalf("after update: key = %+v, want expiration %s and the same public key", again, expires)
	}

	for _, name := range []string{generated, supplied} {
		if _, err := keys.Delete(ctx, name); err != nil {
			t.Fatalf("Delete %s: %v", name, err)
		}
		if _, _, err := keys.Get(ctx, name); !errors.Is(err, cinc.ErrNotFound) {
			t.Fatalf("Get %s after delete: err = %v, want ErrNotFound", name, err)
		}
	}
}

// testKeyRename renames a client key with a PUT whose body carries only the
// new name and expiration. A Chef server moves the key and keeps the public
// key the body leaves out.
func testKeyRename(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	keys := c.Keys.Client(newClient(t, c))
	old := newKey(t, keys, false)
	renamed := old + "-renamed"
	cleanup(t, "key "+renamed, func(ctx context.Context) error {
		_, err := keys.Delete(ctx, renamed)
		return err
	})
	before, _, err := keys.Get(ctx, old)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	const expires = "2099-01-01T00:00:00Z"
	if _, _, err := keys.Update(ctx, old, &cinc.Key{Name: renamed, ExpirationDate: expires}); err != nil {
		t.Fatalf("Update (rename): %v", err)
	}
	moved, _, err := keys.Get(ctx, renamed)
	if err != nil {
		t.Fatalf("Get renamed key: %v", err)
	}
	if moved.ExpirationDate != expires || moved.PublicKey != before.PublicKey {
		t.Fatalf("renamed key = %+v, want expiration %s and the original public key", moved, expires)
	}
	if _, _, err := keys.Get(ctx, old); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get old name after rename: err = %v, want ErrNotFound", err)
	}
}

// newKey adds a uniquely named, non-expiring key to keys — server-generated
// or from a fresh public key — and registers its deletion.
func newKey(t *testing.T, keys *cinc.KeyScope, generate bool) string {
	t.Helper()
	name := uniqueName(t, "key")
	cleanup(t, "key "+name, func(ctx context.Context) error {
		_, err := keys.Delete(ctx, name)
		return err
	})
	k := &cinc.Key{Name: name, ExpirationDate: "infinity"}
	if generate {
		k.CreateKey = true
	} else {
		k.PublicKey = publicKeyPEM(t)
	}
	created, _, err := keys.Create(t.Context(), k)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if generate && created.PrivateKey == "" {
		t.Fatalf("server-generated key came back without a private key: %+v", created)
	}
	return name
}

// publicKeyPEM returns a fresh RSA public key in PEM (PKIX) form.
func publicKeyPEM(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// newClient creates a uniquely named API client and registers its deletion.
func newClient(t *testing.T, c *cinc.Client) string {
	t.Helper()
	name := uniqueName(t, "client")
	cleanup(t, "client "+name, func(ctx context.Context) error {
		_, err := c.Clients.Delete(ctx, name)
		return err
	})
	if _, _, err := c.Clients.Create(t.Context(), &cinc.APIClient{Name: name}); err != nil {
		t.Fatalf("create client: %v", err)
	}
	return name
}

// testKeyCreateDefaults adds a key giving only its name. erchef requires
// expiration_date and one of public_key or create_key; KeyScope.Create fills
// in a server-generated, non-expiring key.
func testKeyCreateDefaults(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	keys := c.Keys.Client(newClient(t, c))
	name := uniqueName(t, "key")
	cleanup(t, "key "+name, func(ctx context.Context) error {
		_, err := keys.Delete(ctx, name)
		return err
	})
	created, _, err := keys.Create(ctx, &cinc.Key{Name: name})
	if err != nil {
		t.Fatalf("Create with only a name: %v", err)
	}
	if created.PrivateKey == "" {
		t.Fatalf("created key = %+v, want a server-generated private key", created)
	}
	got, _, err := keys.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PublicKey == "" || got.ExpirationDate != "infinity" {
		t.Fatalf("key = %+v, want a public key that never expires", got)
	}
}
