// databags_test.go
package cinc

import (
	"context"
	"errors"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestDataBagItem_ID(t *testing.T) {
	if got := (DataBagItem{"id": "x"}).ID(); got != "x" {
		t.Errorf("ID = %q, want x", got)
	}
	if got := (DataBagItem{}).ID(); got != "" {
		t.Errorf("empty item ID = %q, want \"\"", got)
	}
	// A non-string id should not panic — it returns "".
	if got := (DataBagItem{"id": 42}).ID(); got != "" {
		t.Errorf("non-string id returned %q, want \"\"", got)
	}
}

func TestDataBagItems_Create_RequiresID(t *testing.T) {
	srv := cinctest.New(t)
	c := newTestClient(t, srv.Server)
	_, _, err := c.DataBags.Items("creds").Create(context.Background(), DataBagItem{"k": "v"})
	if err == nil {
		t.Fatal("expected error when item has no id")
	}
	if !contains(err.Error(), `"id"`) {
		t.Errorf("error %q should mention \"id\"", err.Error())
	}
}

func TestDataBagItems_Update_RequiresID(t *testing.T) {
	srv := cinctest.New(t)
	c := newTestClient(t, srv.Server)
	_, _, err := c.DataBags.Items("creds").Update(context.Background(), DataBagItem{"k": "v"})
	if err == nil {
		t.Fatal("expected error when item has no id")
	}
}

func TestDataBags_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/data/missing/x", cinctest.Route{
		Status: 404, Body: `{"error":["item not found"]}`,
	})
	c := newTestClient(t, srv.Server)

	_, _, err := c.DataBags.Items("missing").Get(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound chain", err)
	}
}

func TestDataBags(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/data",
		cinctest.Route{Body: `{"creds":"http://x/data/creds"}`})
	srv.Handle("POST /organizations/o/data",
		cinctest.Route{Status: 201, Body: `{"uri":"http://x/data/creds"}`})
	srv.Handle("DELETE /organizations/o/data/creds", cinctest.Route{Body: `{}`})
	srv.Handle("GET /organizations/o/data/creds",
		cinctest.Route{Body: `{"db":"http://x/data/creds/db"}`})
	srv.Handle("GET /organizations/o/data/creds/db",
		cinctest.Route{Body: `{"id":"db","password":"s3cret"}`})
	srv.Handle("POST /organizations/o/data/creds",
		cinctest.Route{Status: 201, Body: `{"id":"web"}`})
	srv.Handle("PUT /organizations/o/data/creds/db",
		cinctest.Route{Body: `{"id":"db","password":"new"}`})
	srv.Handle("DELETE /organizations/o/data/creds/db", cinctest.Route{Body: `{}`})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	if names, _, err := c.DataBags.List(ctx); err != nil || names["creds"] == "" {
		t.Fatalf("List: %+v %v", names, err)
	}
	if _, err := c.DataBags.Create(ctx, "creds"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	items := c.DataBags.Items("creds")
	if names, _, err := items.List(ctx); err != nil || names["db"] == "" {
		t.Fatalf("Items.List: %+v %v", names, err)
	}
	it, _, err := items.Get(ctx, "db")
	if err != nil || it["password"] != "s3cret" {
		t.Fatalf("Items.Get: %+v %v", it, err)
	}
	if _, _, err := items.Create(ctx, DataBagItem{"id": "web"}); err != nil {
		t.Fatalf("Items.Create: %v", err)
	}
	if _, _, err := items.Update(ctx, DataBagItem{"id": "db", "password": "new"}); err != nil {
		t.Fatalf("Items.Update: %v", err)
	}
	if _, err := items.Delete(ctx, "db"); err != nil {
		t.Fatalf("Items.Delete: %v", err)
	}
	if _, err := c.DataBags.Delete(ctx, "creds"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
