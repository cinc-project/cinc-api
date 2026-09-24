package suite

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testDataBagLifecycle(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	bag := newDataBag(t, c)
	items := c.DataBags.Items(bag)
	id := uniqueName(t, "item")

	created, _, err := items.Create(ctx, cinc.DataBagItem{"id": id, "port": 8080.0, "tags": []any{"a", "b"}})
	if err != nil {
		t.Fatalf("Create item: %v", err)
	}
	// A Chef server adds chef_type and data_bag to the item it echoes back;
	// the client removes them, so the returned item is exactly the stored one.
	for _, k := range []string{"chef_type", "data_bag"} {
		if _, ok := created[k]; ok {
			t.Errorf("created item carries server-added key %q: %v", k, created)
		}
	}

	got, _, err := items.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get item: %v", err)
	}
	want := cinc.DataBagItem{"id": id, "port": 8080.0, "tags": []any{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("item = %v, want %v", got, want)
	}

	list, _, err := items.List(ctx)
	if err != nil {
		t.Fatalf("List items: %v", err)
	}
	if _, ok := list[id]; !ok {
		t.Fatalf("%s missing from item list", id)
	}

	got["port"] = 9090.0
	if _, _, err := items.Update(ctx, got); err != nil {
		t.Fatalf("Update item: %v", err)
	}
	again, _, err := items.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get item after update: %v", err)
	}
	if again["port"] != 9090.0 {
		t.Fatalf("after update: item = %v", again)
	}

	if _, err := items.Delete(ctx, id); err != nil {
		t.Fatalf("Delete item: %v", err)
	}
	if _, _, err := items.Get(ctx, id); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get item after delete: err = %v, want ErrNotFound", err)
	}

	if _, err := c.DataBags.Delete(ctx, bag); err != nil {
		t.Fatalf("Delete bag: %v", err)
	}
	bags, _, err := c.DataBags.List(ctx)
	if err != nil {
		t.Fatalf("List bags: %v", err)
	}
	if _, ok := bags[bag]; ok {
		t.Fatalf("%s still listed after delete", bag)
	}
}

// testDataBagEncryptedRoundTrip stores an item encrypted by this client
// (format version 3) and checks that both the item the server echoes back and
// the item read later decrypt to the original values.
func testDataBagEncryptedRoundTrip(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	items := c.DataBags.Items(newDataBag(t, c))
	secret := []byte(randomHex(t, 32))
	id := uniqueName(t, "item")
	plain := cinc.DataBagItem{
		"id":       id,
		"password": "hunter2",
		"port":     5432.0,
		"replicas": map[string]any{"primary": "db1", "standby": []any{"db2", "db3"}},
	}

	enc, err := plain.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	created, _, err := items.Create(ctx, enc)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, _, err := items.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for what, item := range map[string]cinc.DataBagItem{"created": created, "stored": stored} {
		if !item.IsEncrypted() {
			t.Errorf("%s item is not recognised as encrypted: %v", what, item)
			continue
		}
		dec, err := item.Decrypt(secret)
		if err != nil {
			t.Errorf("Decrypt %s item: %v", what, err)
			continue
		}
		if !reflect.DeepEqual(dec, plain) {
			t.Errorf("%s item decrypts to %v, want %v", what, dec, plain)
		}
	}
	if _, err := stored.Decrypt([]byte("wrong secret")); !errors.Is(err, cinc.ErrDataBagAuth) {
		t.Errorf("Decrypt with the wrong secret: err = %v, want ErrDataBagAuth", err)
	}
}

// testDataBagEncryptedEdit runs the encrypted edit flow through the server's
// echo and storage: CreateEncrypted, then GetDecrypted, a plaintext change,
// UpdateEncrypted, and GetDecrypted again. The item is stored encrypted
// throughout, and re-encrypting a stored item is refused before any request.
func testDataBagEncryptedEdit(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	items := c.DataBags.Items(newDataBag(t, c))
	secret := []byte(randomHex(t, 32))
	id := uniqueName(t, "item")
	plain := cinc.DataBagItem{"id": id, "motd": "hello"}

	created, _, err := items.CreateEncrypted(ctx, plain, secret)
	if err != nil {
		t.Fatalf("CreateEncrypted: %v", err)
	}
	if !created.IsEncrypted() {
		t.Errorf("CreateEncrypted returned %v, want the encrypted item", created)
	}
	current, _, err := items.GetDecrypted(ctx, id, secret)
	if err != nil {
		t.Fatalf("GetDecrypted: %v", err)
	}
	if !reflect.DeepEqual(current, plain) {
		t.Errorf("GetDecrypted = %v, want %v", current, plain)
	}

	current["motd"] = "hello again"
	current["port"] = 5432.0
	if _, _, err := items.UpdateEncrypted(ctx, current, secret); err != nil {
		t.Fatalf("UpdateEncrypted: %v", err)
	}
	stored, _, err := items.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !stored.IsEncrypted() {
		t.Errorf("stored item is not encrypted: %v", stored)
	}
	if got := len(stored.Content()); got != 2 {
		t.Errorf("stored item Content has %d keys, want 2: %v", got, stored.Content())
	}
	edited, _, err := items.GetDecrypted(ctx, id, secret)
	if err != nil {
		t.Fatalf("GetDecrypted after update: %v", err)
	}
	if !reflect.DeepEqual(edited, current) {
		t.Errorf("GetDecrypted after update = %v, want %v", edited, current)
	}

	if _, _, err := items.GetDecrypted(ctx, id, []byte("wrong secret")); !errors.Is(err, cinc.ErrDataBagAuth) {
		t.Errorf("GetDecrypted with the wrong secret: err = %v, want ErrDataBagAuth", err)
	}
	if _, _, err := items.UpdateEncrypted(ctx, stored, secret); !errors.Is(err, cinc.ErrAlreadyEncrypted) {
		t.Errorf("UpdateEncrypted of the stored item: err = %v, want ErrAlreadyEncrypted", err)
	}
}

// Encrypted values produced by Chef's own Ruby implementation
// (Chef::EncryptedDataBagItem::Encryptor::Version{1,2,3}Encryptor.new(
// "hello world", chefSecret).for_encrypted_item). The same fixtures back the
// unit tests in the root package's databag_crypto_test.go; here they prove the
// server stores each format unchanged and it still decrypts after the round
// trip. The client itself only writes version 3.
const chefSecret = "opensesame-super-secret-key"

var chefEncryptedHelloWorld = map[string]string{
	"v1": `{"encrypted_data":"MSa0fay80/gnrXL5WOHWRnI2/mFrc5VCp2VsCDJaMTk=\n","iv":"s2ElSTGBePtOJxPuaOdazA==\n","version":1,"cipher":"aes-256-cbc"}`,
	"v2": `{"encrypted_data":"YPayJpcFtqEphi40tnv8QWBwcvpakdxfctIeGOnakHE=\n","hmac":"2SRvwbdKdRejpDkWZfpmrzsG09Cj3QwijFlWMIN3Glw=\n","iv":"YMv0tjSdxQDOXjTHVFa/ew==\n","version":2,"cipher":"aes-256-cbc"}`,
	"v3": `{"encrypted_data":"tmPS0vwip+VU5tXd23ekJIGw0CrikuoKeZGm1mqD\n","iv":"ckznCKqh5nUXxHXB\n","auth_tag":"M5LttZ2UqwNvEWLVRwbAeA==\n","version":3,"cipher":"aes-256-gcm"}`,
}

func testDataBagChefEncryptedFormats(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	items := c.DataBags.Items(newDataBag(t, c))
	for version, raw := range chefEncryptedHelloWorld {
		var wrapper map[string]any
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			t.Fatalf("decode %s fixture: %v", version, err)
		}
		id := uniqueName(t, version)
		if _, _, err := items.Create(ctx, cinc.DataBagItem{"id": id, "greeting": wrapper}); err != nil {
			t.Fatalf("Create %s item: %v", version, err)
		}
		stored, _, err := items.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s item: %v", version, err)
		}
		dec, err := stored.Decrypt([]byte(chefSecret))
		if err != nil {
			t.Errorf("Decrypt %s item: %v", version, err)
			continue
		}
		if dec["greeting"] != "hello world" {
			t.Errorf("%s item decrypts to %v, want greeting \"hello world\"", version, dec)
		}
	}
}

// newDataBag creates a uniquely named data bag and registers its deletion,
// which also removes any items still in it.
func newDataBag(t *testing.T, c *cinc.Client) string {
	t.Helper()
	bag := uniqueName(t, "bag")
	cleanup(t, "data bag "+bag, func(ctx context.Context) error {
		_, err := c.DataBags.Delete(ctx, bag)
		return err
	})
	if _, err := c.DataBags.Create(t.Context(), bag); err != nil {
		t.Fatalf("create data bag: %v", err)
	}
	return bag
}
