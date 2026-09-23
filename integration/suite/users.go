package suite

import (
	"context"
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testUserLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name, _ := newUser(t, c)

	got, _, err := c.Users.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserName != name || got.Email != name+"@example.com" || got.FirstName != "Test" {
		t.Fatalf("user = %+v", got)
	}

	list, _, err := c.Users.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[name]; !ok {
		t.Fatalf("%s missing from user list", name)
	}

	// A Chef server answers a user PUT with {"uri": ...} only, so the update
	// is checked through a fresh Get rather than Update's return value.
	got.DisplayName = "Renamed " + name
	got.LastName = "Updated"
	if _, _, err := c.Users.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _, err := c.Users.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if again.DisplayName != "Renamed "+name || again.LastName != "Updated" || again.Email != got.Email {
		t.Fatalf("after update: user = %+v", again)
	}

	if _, err := c.Users.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := c.Users.Get(ctx, name); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

// testUserAuthenticate checks a user's password through /authenticate_user.
func testUserAuthenticate(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name, _ := newUser(t, c)
	if _, err := c.Users.Authenticate(ctx, name, userPassword(name)); err != nil {
		t.Fatalf("Authenticate with the right password: %v", err)
	}
	if _, err := c.Users.Authenticate(ctx, name, "wrong-password"); err == nil {
		t.Fatal("Authenticate with a wrong password succeeded")
	}
}

// newUser creates a uniquely named user with a server-generated key and
// registers its deletion. It returns the name and the user's private key.
func newUser(t *testing.T, c *cinc.Client) (string, string) {
	t.Helper()
	name := uniqueName(t, "user")
	cleanup(t, "user "+name, func(ctx context.Context) error {
		_, err := c.Users.Delete(ctx, name)
		return err
	})
	res, _, err := c.Users.Create(t.Context(), &cinc.User{
		UserName:    name,
		DisplayName: name,
		Email:       name + "@example.com",
		FirstName:   "Test",
		LastName:    "User",
		Password:    userPassword(name),
		CreateKey:   true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if res.ChefKey.PrivateKey == "" {
		t.Fatalf("user created with create_key came back without a private key: %+v", res)
	}
	return name, res.ChefKey.PrivateKey
}

// userPassword is the password newUser gives name: unique per user, and
// long enough for any server password policy.
func userPassword(name string) string {
	return "pw-" + name + "-Aa1!"
}

// clientAs returns a client for tgt's org that signs as user with the PEM
// private key privateKey.
func clientAs(t *testing.T, tgt Target, user, privateKey string) *cinc.Client {
	t.Helper()
	key, err := cinc.ParseKey([]byte(privateKey))
	if err != nil {
		t.Fatalf("parse %s's key: %v", user, err)
	}
	c, err := cinc.NewClient(cinc.Config{ServerURL: tgt.ServerURL, Org: tgt.Org, ClientName: user, Key: key},
		cinc.WithHTTPClient(tgt.HTTPClient))
	if err != nil {
		t.Fatalf("client for %s: %v", user, err)
	}
	return c
}
