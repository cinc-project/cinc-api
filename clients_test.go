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

	t.Run("delete failure aborts before create", func(t *testing.T) {
		srv := cinctest.New(t)
		var created bool
		srv.Handle("DELETE /organizations/o/clients/ghost/keys/default",
			cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
		srv.Handle("POST /organizations/o/clients/ghost/keys",
			cinctest.Route{Status: 201, Body: `{}`, Assert: func(*testing.T, *http.Request, []byte) { created = true }})

		c := newTestClient(t, srv.Server)
		_, _, err := c.Clients.Reregister(context.Background(), "ghost")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound from the delete", err)
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
