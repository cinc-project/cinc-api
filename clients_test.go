// clients_test.go
package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestClients_CRUD(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/clients/node1",
		cinctest.Route{Body: `{"name":"node1","validator":false}`})
	srv.Handle("POST /organizations/o/clients",
		cinctest.Route{Status: 201,
			Body: `{"uri":"http://x/clients/node2","chef_key":{"private_key":"-----BEGIN"}}`})
	srv.Handle("DELETE /organizations/o/clients/node1", cinctest.Route{Body: `{}`})
	srv.Handle("GET /organizations/o/clients",
		cinctest.Route{Body: `{"node1":"http://x/clients/node1"}`})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	cl, _, err := c.Clients.Get(ctx, "node1")
	if err != nil || cl.Name != "node1" || cl.Validator {
		t.Fatalf("Get: %+v %v", cl, err)
	}
	created, _, err := c.Clients.Create(ctx, &APIClient{Name: "node2"})
	if err != nil || created.ChefKey.PrivateKey == "" {
		t.Fatalf("Create: %+v %v", created, err)
	}
	if _, err := c.Clients.Delete(ctx, "node1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if names, _, err := c.Clients.List(ctx); err != nil || names["node1"] == "" {
		t.Fatalf("List: %+v %v", names, err)
	}
}

// decodeBody unmarshals a request body into a generic map so tests can assert
// on which keys are present on the wire, not just on their values.
func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	return m
}

func TestClients_Create_RequestBody(t *testing.T) {
	t.Run("asks the server to generate a key by default", func(t *testing.T) {
		// Under API v1 erchef creates a keyless client unless create_key or
		// public_key is sent.
		srv := cinctest.New(t)
		srv.Handle("POST /organizations/o/clients", cinctest.Route{
			Status: 201,
			Body:   `{"uri":"http://x/clients/web01","chef_key":{"name":"default","private_key":"-----BEGIN"}}`,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
				m := decodeBody(t, body)
				if m["create_key"] != true {
					t.Errorf("create_key = %v, want true; body %s", m["create_key"], body)
				}
				for _, k := range []string{"chef_key", "public_key"} {
					if _, ok := m[k]; ok {
						t.Errorf("request body carries %q: %s", k, body)
					}
				}
				if m["name"] != "web01" || m["validator"] != false {
					t.Errorf("body = %s, want name=web01 validator=false", body)
				}
			},
		})

		c := newTestClient(t, srv.Server)
		got, _, err := c.Clients.Create(context.Background(), &APIClient{Name: "web01"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got.ChefKey.PrivateKey == "" {
			t.Errorf("Create returned no private key: %+v", got)
		}
	})

	t.Run("sends a supplied public key instead of create_key", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("POST /organizations/o/clients", cinctest.Route{
			Status: 201,
			Body:   `{"uri":"http://x/clients/web02","chef_key":{"name":"default","public_key":"PUB"}}`,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
				m := decodeBody(t, body)
				if m["public_key"] != "PUB" {
					t.Errorf("public_key = %v, want PUB; body %s", m["public_key"], body)
				}
				for _, k := range []string{"create_key", "chef_key"} {
					if _, ok := m[k]; ok {
						t.Errorf("request body carries %q: %s", k, body)
					}
				}
			},
		})

		c := newTestClient(t, srv.Server)
		if _, _, err := c.Clients.Create(context.Background(),
			&APIClient{Name: "web02", PublicKey: "PUB"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})
}

func TestClients_Update_OmitsKeyFields(t *testing.T) {
	// erchef rejects create_key/public_key on a client PUT with
	// key_management_not_supported; keys are managed via the keys API.
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/clients/web01", cinctest.Route{
		Body: `{"name":"web01","validator":true}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			m := decodeBody(t, body)
			for _, k := range []string{"create_key", "public_key", "private_key", "chef_key"} {
				if _, ok := m[k]; ok {
					t.Errorf("update body carries %q: %s", k, body)
				}
			}
			if m["name"] != "web01" || m["validator"] != true {
				t.Errorf("body = %s, want name=web01 validator=true", body)
			}
		},
	})

	c := newTestClient(t, srv.Server)
	cl := &APIClient{Name: "web01", Validator: true, PublicKey: "PUB",
		ChefKey: ChefKey{Name: "default", PrivateKey: "-----BEGIN"}}
	if _, _, err := c.Clients.Update(context.Background(), cl); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestClients_Update(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/clients/monkeypants",
		cinctest.Route{
			Body: `{"name":"monkeypants","clientname":"monkeypants","validator":true,"json_class":"Chef::ApiClient","chef_type":"client"}`,
		})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	updated, _, err := c.Clients.Update(ctx, &APIClient{Name: "monkeypants", Validator: true})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "monkeypants" {
		t.Fatalf("Update: expected name=monkeypants, got %q", updated.Name)
	}
	if !updated.Validator {
		t.Fatalf("Update: expected validator=true, got false")
	}
}

func TestClients_Update_Errors(t *testing.T) {
	t.Run("404_is_ErrNotFound", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("PUT /organizations/o/clients/ghost",
			cinctest.Route{Status: 404, Body: `{"error":["client 'ghost' not found"]}`})
		c := newTestClient(t, srv.Server)
		_, _, err := c.Clients.Update(context.Background(), &APIClient{Name: "ghost"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestClients_Reregister(t *testing.T) {
	t.Run("deletes then recreates the default key", func(t *testing.T) {
		srv := cinctest.New(t)
		var deleted, created bool
		srv.Handle("DELETE /organizations/o/clients/node/keys/default",
			cinctest.Route{Body: `{"name":"default"}`, Assert: func(*testing.T, *http.Request, []byte) { deleted = true }})
		srv.Handle("POST /organizations/o/clients/node/keys", cinctest.Route{
			Status: 201,
			Body:   `{"uri":"http://x/clients/node/keys/default","private_key":"-----BEGIN RSA PRIVATE KEY-----\nNEW\n-----END RSA PRIVATE KEY-----\n"}`,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
				created = true
				var k Key
				if err := json.Unmarshal(body, &k); err != nil {
					t.Fatalf("decode POST: %v", err)
				}
				if k.Name != "default" || !k.CreateKey || k.ExpirationDate != "infinity" {
					t.Errorf("POST body = %+v, want name=default create_key=true expiration=infinity", k)
				}
			},
		})

		c := newTestClient(t, srv.Server)
		got, _, err := c.Clients.Reregister(context.Background(), "node")
		if err != nil {
			t.Fatalf("Reregister: %v", err)
		}
		if !deleted || !created {
			t.Errorf("calls: deleted=%v created=%v, want both true", deleted, created)
		}
		if got.PrivateKey == "" {
			t.Errorf("Reregister returned no private key: %+v", got)
		}
	})

	t.Run("missing default key is created without a delete", func(t *testing.T) {
		// A client created under API v1 without create_key or public_key has
		// no "default" key, so the DELETE 404s; Reregister must still mint one.
		srv := cinctest.New(t)
		srv.Handle("DELETE /organizations/o/clients/keyless/keys/default",
			cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
		srv.Handle("POST /organizations/o/clients/keyless/keys", cinctest.Route{
			Status: 201,
			Body:   `{"uri":"http://x/clients/keyless/keys/default","private_key":"-----BEGIN RSA PRIVATE KEY-----\nNEW\n-----END RSA PRIVATE KEY-----\n"}`,
		})

		c := newTestClient(t, srv.Server)
		got, _, err := c.Clients.Reregister(context.Background(), "keyless")
		if err != nil {
			t.Fatalf("Reregister: %v", err)
		}
		if got.PrivateKey == "" {
			t.Errorf("Reregister returned no private key: %+v", got)
		}
	})

	t.Run("missing client reports ErrNotFound without claiming a delete", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("DELETE /organizations/o/clients/ghost/keys/default",
			cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
		srv.Handle("POST /organizations/o/clients/ghost/keys",
			cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})

		c := newTestClient(t, srv.Server)
		_, _, err := c.Clients.Reregister(context.Background(), "ghost")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if contains(err.Error(), "deleted the old default key") {
			t.Errorf("error %q claims a key was deleted when none existed", err.Error())
		}
	})

	t.Run("delete failure aborts before create", func(t *testing.T) {
		srv := cinctest.New(t)
		var created bool
		srv.Handle("DELETE /organizations/o/clients/node/keys/default",
			cinctest.Route{Status: 403, Body: `{"error":["forbidden"]}`})
		srv.Handle("POST /organizations/o/clients/node/keys",
			cinctest.Route{Status: 201, Body: `{}`, Assert: func(*testing.T, *http.Request, []byte) { created = true }})

		c := newTestClient(t, srv.Server)
		_, _, err := c.Clients.Reregister(context.Background(), "node")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden from the delete", err)
		}
		if created {
			t.Error("create was attempted after the delete failed")
		}
	})

	t.Run("create failure is wrapped with recovery guidance", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("DELETE /organizations/o/clients/node/keys/default",
			cinctest.Route{Body: `{"name":"default"}`})
		srv.Handle("POST /organizations/o/clients/node/keys",
			cinctest.Route{Status: 500, Body: `{"error":["boom"]}`})

		c := newTestClient(t, srv.Server)
		_, _, err := c.Clients.Reregister(context.Background(), "node")
		if err == nil {
			t.Fatal("expected an error when the recreate fails")
		}
		if !contains(err.Error(), "deleted the old default key") {
			t.Errorf("error %q should explain the non-atomic recovery path", err.Error())
		}
	})
}
