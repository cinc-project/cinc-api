package suite

import (
	"context"
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testOrgLifecycle creates, reads, lists, renames (full_name) and deletes an
// organization. Creating one returns the new validator client's key.
func testOrgLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "org")
	cleanup(t, "organization "+name, func(ctx context.Context) error {
		_, err := c.Orgs.Delete(ctx, name)
		return err
	})

	res, _, err := c.Orgs.Create(ctx, &cinc.Org{Name: name, FullName: "Test " + name})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ClientName != name+"-validator" || res.PrivateKey == "" {
		t.Fatalf("create result = %+v, want validator %s-validator with a private key", res, name)
	}

	got, _, err := c.Orgs.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != name || got.FullName != "Test "+name {
		t.Fatalf("org = %+v", got)
	}

	list, _, err := c.Orgs.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[name]; !ok {
		t.Fatalf("%s missing from organization list", name)
	}

	got.FullName = "Renamed " + name
	if _, _, err := c.Orgs.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _, err := c.Orgs.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if again.FullName != "Renamed "+name {
		t.Fatalf("after update: org = %+v", again)
	}

	if _, err := c.Orgs.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := c.Orgs.Get(ctx, name); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}
