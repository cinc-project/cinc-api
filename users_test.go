package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestUsers_List(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users", cinctest.Route{
		Body: `{"alice":"http://x/users/alice","bob":"http://x/users/bob"}`,
	})
	c := newTestClient(t, srv.Server)

	list, _, err := c.Users.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list["alice"] == "" || list["bob"] == "" {
		t.Fatalf("List = %+v", list)
	}
}

func TestUsers_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/alice", cinctest.Route{
		Body: `{
			"username":"alice","display_name":"Alice","email":"alice@example.com",
			"first_name":"Alice","last_name":"Smith"
		}`,
	})
	c := newTestClient(t, srv.Server)
	u, _, err := c.Users.Get(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if u.UserName != "alice" || u.Email != "alice@example.com" {
		t.Errorf("User = %+v", u)
	}
}

func TestUsers_Create_ServerGeneratedKey(t *testing.T) {
	// With CreateKey=true the server returns a chef_key with private_key.
	srv := cinctest.New(t)
	srv.Handle("POST /users", cinctest.Route{
		Status: 201,
		Body: `{
			"uri":"http://x/users/alice",
			"chef_key":{
				"name":"default",
				"public_key":"PK",
				"private_key":"-----BEGIN RSA PRIVATE KEY-----\nGEN\n-----END RSA PRIVATE KEY-----\n",
				"expiration_date":"infinity"
			}
		}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req map[string]any
			json.Unmarshal(body, &req)
			if req["username"] != "alice" || req["password"] != "s3cret" {
				t.Errorf("POST body = %v", req)
			}
			if req["create_key"] != true {
				t.Errorf("create_key = %v, want true", req["create_key"])
			}
		},
	})
	c := newTestClient(t, srv.Server)

	res, _, err := c.Users.Create(context.Background(), &User{
		UserName:    "alice",
		DisplayName: "Alice",
		Email:       "alice@example.com",
		FirstName:   "Alice",
		LastName:    "Smith",
		Password:    "s3cret",
		CreateKey:   true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.URI == "" {
		t.Errorf("URI missing")
	}
	if res.ChefKey.PrivateKey == "" {
		t.Fatal("expected chef_key.private_key from server-generated create")
	}
}

func TestUsers_Update(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /users/alice", cinctest.Route{
		Body: `{"username":"alice","display_name":"Alice Renamed","email":"alice@example.com"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]any
			json.Unmarshal(body, &got)
			if got["display_name"] != "Alice Renamed" {
				t.Errorf("PUT body display_name = %v", got["display_name"])
			}
		},
	})
	c := newTestClient(t, srv.Server)
	updated, _, err := c.Users.Update(context.Background(), &User{
		UserName: "alice", DisplayName: "Alice Renamed", Email: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DisplayName != "Alice Renamed" {
		t.Errorf("DisplayName = %q", updated.DisplayName)
	}
}

func TestUsers_Delete(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /users/alice", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.Users.Delete(context.Background(), "alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestUsers_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/missing",
		cinctest.Route{Status: 404, Body: `{"error":["no such user"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Users.Get(context.Background(), "missing"); err == nil {
		t.Fatal("expected 404")
	}
}

func TestUsers_PathsAreTopLevel(t *testing.T) {
	// /users is NOT org-scoped — must not be prefixed with /organizations/o.
	srv := cinctest.New(t)
	srv.Handle("GET /users", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Users.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUsers_Authenticate_OK(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /authenticate_user", cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]string
			json.Unmarshal(body, &got)
			if got["username"] != "grantmc" || got["password"] != "p@ss" {
				t.Errorf("POST body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if _, err := c.Users.Authenticate(context.Background(), "grantmc", "p@ss"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

func TestUsers_Authenticate_BadPassword(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /authenticate_user",
		cinctest.Route{Status: 401, Body: `{"error":["Failed to authenticate."]}`})
	c := newTestClient(t, srv.Server)
	_, err := c.Users.Authenticate(context.Background(), "grantmc", "wrong")
	if err == nil {
		t.Fatal("expected 401")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}
