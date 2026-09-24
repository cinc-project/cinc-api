// databags_test.go
package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func TestDataBagItem_Validate(t *testing.T) {
	if err := (DataBagItem{"id": "x"}).Validate(); err != nil {
		t.Errorf("Validate on a valid item: %v", err)
	}
	for _, item := range []DataBagItem{nil, {}, {"k": "v"}, {"id": ""}, {"id": 42}, {"id": nil}} {
		if err := item.Validate(); !errors.Is(err, ErrMissingDataBagItemID) {
			t.Errorf("Validate(%v) = %v, want ErrMissingDataBagItemID", item, err)
		}
	}
}

// Create and Update refuse an item without an id before any request is sent;
// the harness fails the test on an unhandled request.
func TestDataBagItems_CreateUpdate_Validate(t *testing.T) {
	srv := cinctest.New(t)
	c := newTestClient(t, srv.Server)
	items := c.DataBags.Items("creds")
	ctx := context.Background()
	if _, _, err := items.Create(ctx, DataBagItem{"id": 1}); !errors.Is(err, ErrMissingDataBagItemID) {
		t.Errorf("Create err = %v, want ErrMissingDataBagItemID", err)
	}
	if _, _, err := items.Update(ctx, DataBagItem{"id": 1}); !errors.Is(err, ErrMissingDataBagItemID) {
		t.Errorf("Update err = %v, want ErrMissingDataBagItemID", err)
	}
}

func TestDataBagItem_Content(t *testing.T) {
	wrapped := map[string]any{"version": 3.0, "encrypted_data": "x", "iv": "y", "auth_tag": "z"}
	tests := []struct {
		name string
		item DataBagItem
		want map[string]any
	}{
		{"nil", nil, map[string]any{}},
		{"only id", DataBagItem{"id": "x"}, map[string]any{}},
		{"plaintext", DataBagItem{"id": "x", "port": 1.0, "tags": []any{"a"}}, map[string]any{"port": 1.0, "tags": []any{"a"}}},
		{"server keys dropped",
			DataBagItem{"id": "x", "port": 1.0, "chef_type": "data_bag_item", "data_bag": "creds"},
			map[string]any{"port": 1.0}},
		// Decrypt treats an encrypted value under a server key name as real
		// data, so Content keeps it too.
		{"encrypted server key name kept",
			DataBagItem{"id": "x", "data_bag": wrapped, "chef_type": "data_bag_item"},
			map[string]any{"data_bag": wrapped}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.item.Content(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Content() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDataBagItem_Content_IsACopy(t *testing.T) {
	item := DataBagItem{"id": "x", "port": 1.0}
	item.Content()["port"] = 2.0
	if item["port"] != 1.0 {
		t.Errorf("Content aliased the item: %v", item)
	}
}

// encryptedEcho returns the JSON erchef answers a POST/PUT of item with: the
// item plus its server-added keys.
func encryptedEcho(t *testing.T, item DataBagItem) string {
	t.Helper()
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "}") + `,"chef_type":"data_bag_item","data_bag":"creds"}`
}

func TestDataBagItems_GetDecrypted(t *testing.T) {
	secret := []byte("s3cret")
	plain := DataBagItem{"id": "db", "password": "hunter2", "port": 5432.0}
	enc, err := plain.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(enc)
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/data/creds/db", cinctest.Route{Body: string(body)})
	srv.Handle("GET /organizations/o/data/creds/plain", cinctest.Route{Body: `{"id":"plain","password":"hunter2"}`})
	srv.Handle("GET /organizations/o/data/creds/missing", cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)
	items := c.DataBags.Items("creds")
	ctx := context.Background()

	got, resp, err := items.GetDecrypted(ctx, "db", secret)
	if err != nil {
		t.Fatalf("GetDecrypted: %v", err)
	}
	if resp == nil || resp.StatusCode != 200 {
		t.Errorf("GetDecrypted response = %+v, want the 200", resp)
	}
	if !reflect.DeepEqual(got, plain) {
		t.Errorf("GetDecrypted = %v, want %v", got, plain)
	}

	if got, _, err := items.GetDecrypted(ctx, "db", []byte("wrong")); !errors.Is(err, ErrDataBagAuth) || got != nil {
		t.Errorf("wrong secret: %v, %v; want nil, ErrDataBagAuth", got, err)
	}
	if got, _, err := items.GetDecrypted(ctx, "plain", secret); !errors.Is(err, ErrNotEncrypted) || got != nil {
		t.Errorf("plaintext item: %v, %v; want nil, ErrNotEncrypted", got, err)
	}
	if _, _, err := items.GetDecrypted(ctx, "missing", secret); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item: err = %v, want ErrNotFound", err)
	}
}

func TestDataBagItems_CreateUpdateEncrypted(t *testing.T) {
	secret := []byte("s3cret")
	plain := DataBagItem{"id": "db", "password": "hunter2"}
	// The echo only has to be an encrypted item; the server-added keys
	// check that the write path still strips them.
	echo := encryptedEcho(t, mustEncrypt(t, plain, secret))
	sent := map[string]DataBagItem{}
	record := func(method string) func(*testing.T, *http.Request, []byte) {
		return func(t *testing.T, _ *http.Request, body []byte) {
			var item DataBagItem
			if err := json.Unmarshal(body, &item); err != nil {
				t.Errorf("decode %s body: %v", method, err)
			}
			sent[method] = item
		}
	}
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/data/creds", cinctest.Route{Status: 201, Body: echo, Assert: record("POST")})
	srv.Handle("PUT /organizations/o/data/creds/db", cinctest.Route{Body: echo, Assert: record("PUT")})
	c := newTestClient(t, srv.Server)
	items := c.DataBags.Items("creds")
	ctx := context.Background()

	for method, call := range map[string]func() (DataBagItem, *Response, error){
		"POST": func() (DataBagItem, *Response, error) { return items.CreateEncrypted(ctx, plain, secret) },
		"PUT":  func() (DataBagItem, *Response, error) { return items.UpdateEncrypted(ctx, plain, secret) },
	} {
		got, _, err := call()
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if s := sent[method]; !s.IsEncrypted() || s.ID() != "db" {
			t.Errorf("%s sent %v, want an encrypted item with id db", method, s)
		}
		if dec, err := sent[method].Decrypt(secret); err != nil || !reflect.DeepEqual(dec, plain) {
			t.Errorf("%s sent an item decrypting to %v, %v; want %v", method, dec, err, plain)
		}
		if _, ok := got["chef_type"]; ok || !got.IsEncrypted() {
			t.Errorf("%s returned %v, want the encrypted echo without server keys", method, got)
		}
	}
	if plain["password"] != "hunter2" {
		t.Errorf("the caller's item was modified: %v", plain)
	}
}

// CreateEncrypted and UpdateEncrypted fail before sending anything when the
// item cannot be encrypted: no id, or already encrypted.
func TestDataBagItems_CreateUpdateEncrypted_RefuseBeforeSending(t *testing.T) {
	secret := []byte("s3cret")
	srv := cinctest.New(t)
	c := newTestClient(t, srv.Server)
	items := c.DataBags.Items("creds")
	ctx := context.Background()
	enc := mustEncrypt(t, DataBagItem{"id": "db", "password": "hunter2"}, secret)

	for name, call := range map[string]func(DataBagItem) error{
		"CreateEncrypted": func(i DataBagItem) error { _, _, err := items.CreateEncrypted(ctx, i, secret); return err },
		"UpdateEncrypted": func(i DataBagItem) error { _, _, err := items.UpdateEncrypted(ctx, i, secret); return err },
	} {
		if err := call(DataBagItem{"password": "x"}); !errors.Is(err, ErrMissingDataBagItemID) {
			t.Errorf("%s without id: err = %v, want ErrMissingDataBagItemID", name, err)
		}
		if err := call(enc); !errors.Is(err, ErrAlreadyEncrypted) {
			t.Errorf("%s of an encrypted item: err = %v, want ErrAlreadyEncrypted", name, err)
		}
	}
}

func mustEncrypt(t *testing.T, item DataBagItem, secret []byte) DataBagItem {
	t.Helper()
	enc, err := item.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}
