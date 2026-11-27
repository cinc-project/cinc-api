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

// erchefClientCreateBody is erchef's answer to POST /clients under API v1:
// only the new client's uri, with the default key embedded as chef_key
// (oc_chef_wm_base:create_from_json, oc_chef_wm_key_base:create_object and
// finalize_create_body). The client's name and validator flag are not echoed.
const erchefClientCreateBody = `{
	"uri": "https://chef.example/organizations/o/clients/node2",
	"chef_key": {
		"name": "default",
		"public_key": "-----BEGIN PUBLIC KEY-----\nPUB\n-----END PUBLIC KEY-----\n",
		"expiration_date": "infinity",
		"uri": "https://chef.example/organizations/o/clients/node2/keys/default",
		"private_key": "-----BEGIN RSA PRIVATE KEY-----\nPRIV\n-----END RSA PRIVATE KEY-----\n"
	}
}`

func TestClients_CRUD(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/clients/node1",
		cinctest.Route{Body: `{"name":"node1","validator":false}`})
	srv.Handle("POST /organizations/o/clients",
		cinctest.Route{Status: 201, Body: erchefClientCreateBody})
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
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := ClientCreateResult{
		URI: "https://chef.example/organizations/o/clients/node2",
		ChefKey: ChefKey{
			Name:           "default",
			PublicKey:      "-----BEGIN PUBLIC KEY-----\nPUB\n-----END PUBLIC KEY-----\n",
			PrivateKey:     "-----BEGIN RSA PRIVATE KEY-----\nPRIV\n-----END RSA PRIVATE KEY-----\n",
			ExpirationDate: "infinity",
			URI:            "https://chef.example/organizations/o/clients/node2/keys/default",
		},
	}
	if *created != want {
		t.Fatalf("Create = %+v, want %+v", *created, want)
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
			Body:   erchefClientCreateBody,
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
			// With a supplied public key there is no private_key to embed.
			Body: `{"uri":"https://chef.example/organizations/o/clients/web02","chef_key":{"name":"default","public_key":"PUB","expiration_date":"infinity","uri":"https://chef.example/organizations/o/clients/web02/keys/default"}}`,
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
		got, _, err := c.Clients.Create(context.Background(),
			&APIClient{Name: "web02", PublicKey: "PUB"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got.ChefKey.PrivateKey != "" || got.ChefKey.PublicKey != "PUB" || got.URI == "" {
			t.Errorf("Create = %+v, want the supplied public key and no private key", got)
		}
	})
}

// erchefClientPutBody is erchef's answer to a client PUT: the request body it
// validated, with name and clientname both set (chef_client:destination_name),
// validator filled in from the stored client when absent, plus json_class and
// chef_type (oc_chef_wm_base:update_from_json; chef_wm_named_client has no
// finalize_update_body).
func erchefClientPutBody(name string, validator bool) string {
	b, _ := json.Marshal(map[string]any{
		"name": name, "clientname": name, "validator": validator,
		"json_class": "Chef::ApiClient", "chef_type": "client",
	})
	return string(b)
}

func TestClients_Update_OmitsKeyFields(t *testing.T) {
	// erchef rejects create_key/public_key on a client PUT with
	// key_management_not_supported; keys are managed via the keys API.
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/clients/web01", cinctest.Route{
		Body: erchefClientPutBody("web01", true),
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
	cl := &APIClient{Name: "web01", Validator: true, PublicKey: "PUB"}
	if _, _, err := c.Clients.Update(context.Background(), "web01", cl); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestClients_Update(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/clients/monkeypants",
		cinctest.Route{Body: erchefClientPutBody("monkeypants", true)})

	c := newTestClient(t, srv.Server)
	updated, _, err := c.Clients.Update(context.Background(), "monkeypants",
		&APIClient{Name: "monkeypants", Validator: true})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "monkeypants" || !updated.Validator {
		t.Fatalf("Update = %+v, want name=monkeypants validator=true", updated)
	}
}

// A PUT whose body names a different client renames it: erchef answers 201
// with a Location for the new name (oc_chef_wm_base:update_from_json ->
// handle_rename) and echoes the body.
func TestClients_Update_Rename(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/clients/old", cinctest.Route{
		Status: 201,
		Body:   erchefClientPutBody("new", false),
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			if m := decodeBody(t, body); m["name"] != "new" {
				t.Errorf("body = %s, want name=new", body)
			}
		},
	})

	c := newTestClient(t, srv.Server)
	updated, resp, err := c.Clients.Update(context.Background(), "old", &APIClient{Name: "new"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "new" || resp.StatusCode != http.StatusCreated {
		t.Fatalf("Update = %+v (status %d), want name=new and 201", updated, resp.StatusCode)
	}
}

func TestClients_Update_EmptyBodyNameKeepsName(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/clients/web01", cinctest.Route{
		Body: erchefClientPutBody("web01", true),
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			if m := decodeBody(t, body); m["name"] != "web01" {
				t.Errorf("body = %s, want name=web01 from the path", body)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	cl := &APIClient{Validator: true}
	if _, _, err := c.Clients.Update(context.Background(), "web01", cl); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if cl.Name != "" {
		t.Errorf("Update modified its argument: %+v", cl)
	}
}

func TestClients_Update_Errors(t *testing.T) {
	t.Run("404_is_ErrNotFound", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("PUT /organizations/o/clients/ghost",
			cinctest.Route{Status: 404, Body: `{"error":["client 'ghost' not found"]}`})
		c := newTestClient(t, srv.Server)
		_, _, err := c.Clients.Update(context.Background(), "ghost", &APIClient{Name: "ghost"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("empty name is refused before any request", func(t *testing.T) {
		srv := cinctest.New(t)
		c := newTestClient(t, srv.Server)
		if _, _, err := c.Clients.Update(context.Background(), "", &APIClient{Name: "x"}); err == nil {
			t.Fatal("Update with an empty name succeeded")
		}
	})
}

// erchefRegenerateBody is erchef's answer to PUT .../keys/default with
// {"name":"default","create_key":true}: the request body less create_key, plus
// the generated pair (oc_chef_wm_named_key:finalize_update_body). There is no
// uri and no expiration_date; the stored expiration is kept
// (chef_key:update_from_ejson).
const erchefRegenerateBody = `{"name":"default","private_key":"-----BEGIN RSA PRIVATE KEY-----\nNEW\n-----END RSA PRIVATE KEY-----\n","public_key":"-----BEGIN PUBLIC KEY-----\nNEWPUB\n-----END PUBLIC KEY-----\n"}`

func TestClients_Reregister(t *testing.T) {
	refuseCreate := cinctest.Route{
		Status: 201, Body: `{}`,
		Assert: func(t *testing.T, _ *http.Request, _ []byte) { t.Error("Reregister POSTed a new key") },
	}

	t.Run("regenerates the default key in place", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("PUT /organizations/o/clients/node/keys/default", cinctest.Route{
			Body: erchefRegenerateBody,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
				m := decodeBody(t, body)
				if m["create_key"] != true || m["name"] != "default" || len(m) != 2 {
					t.Errorf("PUT body = %s, want only name=default and create_key=true", body)
				}
			},
		})
		srv.Handle("POST /organizations/o/clients/node/keys", refuseCreate)

		c := newTestClient(t, srv.Server)
		got, resp, err := c.Clients.Reregister(context.Background(), "node")
		if err != nil {
			t.Fatalf("Reregister: %v", err)
		}
		if got.PrivateKey == "" || got.PublicKey == "" || got.Name != "default" || resp.StatusCode != http.StatusOK {
			t.Errorf("Reregister = %+v (status %d), want the new pair", got, resp.StatusCode)
		}
	})

	t.Run("missing default key is created", func(t *testing.T) {
		// A client created under API v1 without create_key or public_key has
		// no "default" key, so the PUT 404s (oc_chef_wm_named_key:auth_info,
		// key_not_found_response); Reregister must still mint one.
		srv := cinctest.New(t)
		srv.Handle("PUT /organizations/o/clients/keyless/keys/default",
			cinctest.Route{Status: 404, Body: `{"error":["Could not find key 'default' for client 'keyless'"]}`})
		srv.Handle("POST /organizations/o/clients/keyless/keys", cinctest.Route{
			Status: 201,
			Body:   `{"uri":"https://chef.example/organizations/o/clients/keyless/keys/default","private_key":"-----BEGIN RSA PRIVATE KEY-----\nNEW\n-----END RSA PRIVATE KEY-----\n"}`,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
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
		got, resp, err := c.Clients.Reregister(context.Background(), "keyless")
		if err != nil {
			t.Fatalf("Reregister: %v", err)
		}
		if got.PrivateKey == "" || resp.StatusCode != http.StatusCreated {
			t.Errorf("Reregister = %+v (status %d), want the created key", got, resp.StatusCode)
		}
	})

	t.Run("missing client reports ErrNotFound", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("PUT /organizations/o/clients/ghost/keys/default",
			cinctest.Route{Status: 404, Body: `{"error":["Cannot load client ghost"]}`})
		srv.Handle("POST /organizations/o/clients/ghost/keys",
			cinctest.Route{Status: 404, Body: `{"error":["Cannot load client ghost"]}`})

		c := newTestClient(t, srv.Server)
		if _, _, err := c.Clients.Reregister(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("other PUT failures are returned without a create", func(t *testing.T) {
		// erchef refuses with 403 when the request is signed with the key
		// being regenerated (oc_chef_wm_named_key:auth_info, key_in_use).
		srv := cinctest.New(t)
		srv.Handle("PUT /organizations/o/clients/node/keys/default",
			cinctest.Route{Status: 403, Body: `{"error":"The key 'default' was used to authenticate this request and cannot be modified or deleted."}`})
		srv.Handle("POST /organizations/o/clients/node/keys", refuseCreate)

		c := newTestClient(t, srv.Server)
		if _, _, err := c.Clients.Reregister(context.Background(), "node"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden from the PUT", err)
		}
	})
}
