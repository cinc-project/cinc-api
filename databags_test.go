// databags_test.go
package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

// A real Chef Server (erchef) answers a data bag item POST or PUT with the
// item it stored plus two keys it adds itself, "chef_type":"data_bag_item"
// and "data_bag":"<bag>" (chef_data_bag_item:add_type_and_bag, called from
// chef_wm_named_data:finalize_create_body and
// chef_wm_named_data_item:finalize_update_body). A GET returns the stored
// item without them. These fixtures reproduce that server behaviour.
func TestDataBagItems_CreateUpdate_StripServerAddedKeys(t *testing.T) {
	secret := []byte("s3cret")
	enc, err := DataBagItem{"id": "db", "password": "hunter2"}.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	// The erchef response body: the request item plus the two added keys.
	respBody := strings.TrimSuffix(string(stored), "}") +
		`,"chef_type":"data_bag_item","data_bag":"creds"}`

	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/data/creds", cinctest.Route{Status: 201, Body: respBody})
	srv.Handle("PUT /organizations/o/data/creds/db", cinctest.Route{Body: respBody})
	c := newTestClient(t, srv.Server)
	items := c.DataBags.Items("creds")
	ctx := context.Background()

	for name, call := range map[string]func() (DataBagItem, *Response, error){
		"Create": func() (DataBagItem, *Response, error) { return items.Create(ctx, enc) },
		"Update": func() (DataBagItem, *Response, error) { return items.Update(ctx, enc) },
	} {
		got, _, err := call()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, k := range []string{"chef_type", "data_bag"} {
			if _, ok := got[k]; ok {
				t.Errorf("%s: returned item still carries server-added %q: %v", name, k, got)
			}
		}
		if len(got) != len(enc) {
			t.Errorf("%s: returned item has keys %v, want exactly those sent", name, got)
		}
		if !got.IsEncrypted() {
			t.Errorf("%s: returned item not reported as encrypted", name)
		}
		plain, err := got.Decrypt(secret)
		if err != nil {
			t.Fatalf("%s: Decrypt: %v", name, err)
		}
		if plain["password"] != "hunter2" {
			t.Errorf("%s: decrypted password = %v", name, plain["password"])
		}
	}
}

// erchef stores whatever unwrapped item it is sent, so an item may carry its
// own "data_bag" or "chef_type" key; the server stores that value but
// overwrites it in the POST/PUT response. The client restores the value it
// sent, because that is what the server actually stored.
func TestDataBagItems_Create_KeepsCallerSuppliedKeys(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/data/creds", cinctest.Route{Status: 201,
		Body: `{"id":"web","data_bag":"creds","chef_type":"data_bag_item"}`})
	c := newTestClient(t, srv.Server)

	got, _, err := c.DataBags.Items("creds").Create(context.Background(),
		DataBagItem{"id": "web", "data_bag": "mine"})
	if err != nil {
		t.Fatal(err)
	}
	want := DataBagItem{"id": "web", "data_bag": "mine"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Create returned %v, want %v", got, want)
	}
}

func TestDataBagItems_Create_EmptyResponseBody(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/data/creds", cinctest.Route{Status: 201})
	c := newTestClient(t, srv.Server)

	got, _, err := c.DataBags.Items("creds").Create(context.Background(),
		DataBagItem{"id": "web", "data_bag": "mine"})
	if err != nil || got != nil {
		t.Errorf("Create = %v, %v; want nil item, nil error", got, err)
	}
}
