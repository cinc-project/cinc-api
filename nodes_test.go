// nodes_test.go
package cinc

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// TestNodes_Errors covers the error paths for the Nodes service.
func TestNodes_Errors(t *testing.T) {
	t.Run("404_is_ErrNotFound", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/missing",
			cinctest.Route{Status: 404, Body: `{"error":["node 'missing' not found"]}`})
		c := newTestClient(t, srv.Server)
		_, _, err := c.Nodes.Get(context.Background(), "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("409_is_ErrConflict", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("POST /organizations/o/nodes",
			cinctest.Route{Status: 409, Body: `{"error":["node already exists"]}`})
		c := newTestClient(t, srv.Server)
		_, _, err := c.Nodes.Create(context.Background(), &Node{Name: "dup"})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("403_is_ErrForbidden", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("DELETE /organizations/o/nodes/locked",
			cinctest.Route{Status: 403, Body: `{"error":["not authorized"]}`})
		c := newTestClient(t, srv.Server)
		_, err := c.Nodes.Delete(context.Background(), "locked")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("malformed_json_200", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/bad",
			cinctest.Route{Status: 200, Body: `{not valid json`})
		c := newTestClient(t, srv.Server)
		_, _, err := c.Nodes.Get(context.Background(), "bad")
		if err == nil {
			t.Fatal("expected decode error for malformed JSON, got nil")
		}
	})

	t.Run("context_cancelled", func(t *testing.T) {
		srv := cinctest.New(t)
		// No route needed — cancelled context should fail before reaching server.
		c := newTestClient(t, srv.Server)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := c.Nodes.Get(ctx, "web01")
		if err == nil {
			t.Fatal("expected error for cancelled context, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Logf("err = %v (not context.Canceled, but still an error — OK)", err)
		}
	})
}

func TestNodes_CRUD(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/web01",
		cinctest.Route{Body: `{"name":"web01","chef_environment":"prod","run_list":["recipe[nginx]"],"automatic":{"fqdn":"web01.x"}}`})
	srv.Handle("POST /organizations/o/nodes",
		cinctest.Route{Status: 201, Body: `{"uri":"http://x/nodes/web02"}`,
			Assert: func(t *testing.T, r *http.Request, body []byte) {
				if !contains(string(body), "web02") {
					t.Errorf("create body missing name: %s", body)
				}
			}})
	srv.Handle("PUT /organizations/o/nodes/web01",
		cinctest.Route{Body: `{"name":"web01","chef_environment":"staging"}`})
	srv.Handle("DELETE /organizations/o/nodes/web01", cinctest.Route{Body: `{}`})
	srv.Handle("GET /organizations/o/nodes",
		cinctest.Route{Body: `{"web01":"http://x/nodes/web01"}`})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	n, _, err := c.Nodes.Get(ctx, "web01")
	if err != nil || n.Environment != "prod" ||
		n.Automatic.GetString("fqdn") != "web01.x" {
		t.Fatalf("Get: %+v %v", n, err)
	}
	if _, _, err := c.Nodes.Create(ctx, &Node{Name: "web02"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := c.Nodes.Update(ctx, &Node{Name: "web01", Environment: "staging"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := c.Nodes.Delete(ctx, "web01"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	names, _, err := c.Nodes.List(ctx)
	if err != nil || names["web01"] == "" {
		t.Fatalf("List: %+v %v", names, err)
	}
}
