package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestKeys_ClientCRUD(t *testing.T) {
	srv := cinctest.New(t)
	const listBody = `[{"name":"default","uri":"http://x/clients/node/keys/default","expired":false}]`
	const getBody = `{"name":"default","public_key":"-----BEGIN PUBLIC KEY-----\nAAA\n-----END PUBLIC KEY-----\n","expiration_date":"infinity"}`
	srv.Handle("GET /organizations/o/clients/node/keys",
		cinctest.Route{Body: listBody})
	srv.Handle("GET /organizations/o/clients/node/keys/default",
		cinctest.Route{Body: getBody})
	srv.Handle("POST /organizations/o/clients/node/keys", cinctest.Route{
		Status: 201,
		Body:   `{"uri":"http://x/clients/node/keys/k1"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req map[string]any
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode POST: %v", err)
			}
			if req["name"] != "k1" || req["expiration_date"] != "infinity" {
				t.Errorf("POST body = %v, want name=k1, expiration_date=infinity", req)
			}
		},
	})
	srv.Handle("PUT /organizations/o/clients/node/keys/default",
		cinctest.Route{Body: `{"name":"default","public_key":"NEW","expiration_date":"2030-01-01T00:00:00Z"}`})
	srv.Handle("DELETE /organizations/o/clients/node/keys/default",
		cinctest.Route{Body: `{}`})

	c := newTestClient(t, srv.Server)
	keys := c.Keys.Client("node")
	ctx := context.Background()

	list, _, err := keys.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "default" || list[0].URI == "" {
		t.Fatalf("List = %+v", list)
	}

	k, _, err := keys.Get(ctx, "default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if k.PublicKey == "" || k.ExpirationDate != "infinity" {
		t.Fatalf("Get = %+v", k)
	}

	created, _, err := keys.Create(ctx, &Key{
		Name: "k1", PublicKey: "-----BEGIN PUBLIC KEY-----\nBBB\n-----END PUBLIC KEY-----\n",
		ExpirationDate: "infinity",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.URI == "" {
		t.Errorf("Create response missing URI: %+v", created)
	}

	updated, _, err := keys.Update(ctx, "default", &Key{
		PublicKey: "NEW", ExpirationDate: "2030-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.PublicKey != "NEW" {
		t.Errorf("Update returned %+v", updated)
	}

	if _, err := keys.Delete(ctx, "default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestKeys_UserCRUD(t *testing.T) {
	// User keys live at /users/USER/keys (NOT org-scoped).
	srv := cinctest.New(t)
	srv.Handle("GET /users/alice/keys",
		cinctest.Route{Body: `[{"name":"default","uri":"http://x/users/alice/keys/default","expired":true}]`})
	srv.Handle("GET /users/alice/keys/default",
		cinctest.Route{Body: `{"name":"default","public_key":"PK","expiration_date":"2024-01-01T00:00:00Z"}`})
	c := newTestClient(t, srv.Server)
	keys := c.Keys.User("alice")
	ctx := context.Background()

	list, _, err := keys.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || !list[0].Expired {
		t.Fatalf("List = %+v", list)
	}

	k, _, err := keys.Get(ctx, "default")
	if err != nil || k.PublicKey != "PK" {
		t.Fatalf("Get: %+v %v", k, err)
	}
}

func TestKeys_ServerGeneratedReturnsPrivateKey(t *testing.T) {
	// With CreateKey=true the server generates the keypair and returns
	// private_key in the response.
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/clients/node/keys", cinctest.Route{
		Status: 201,
		Body:   `{"uri":"http://x/clients/node/keys/k2","private_key":"-----BEGIN RSA PRIVATE KEY-----\nGEN\n-----END RSA PRIVATE KEY-----\n"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req map[string]any
			json.Unmarshal(body, &req)
			if req["create_key"] != true {
				t.Errorf("POST body create_key = %v, want true", req["create_key"])
			}
		},
	})
	c := newTestClient(t, srv.Server)
	created, _, err := c.Keys.Client("node").Create(context.Background(), &Key{
		Name: "k2", CreateKey: true, ExpirationDate: "infinity",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.PrivateKey == "" {
		t.Fatal("expected private_key in server-generated response")
	}
}

func TestKeys_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/clients/node/keys/missing",
		cinctest.Route{Status: 404, Body: `{"error":["key not found"]}`})
	c := newTestClient(t, srv.Server)
	_, _, err := c.Keys.Client("node").Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected 404")
	}
}
