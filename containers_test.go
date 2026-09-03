package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestContainers_List(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/containers", cinctest.Route{
		Body: `{
			"nodes":"http://x/containers/nodes",
			"clients":"http://x/containers/clients"
		}`,
	})
	c := newTestClient(t, srv.Server)

	list, _, err := c.Containers.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list["nodes"] == "" || list["clients"] == "" {
		t.Fatalf("List = %+v", list)
	}
}

func TestContainers_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/containers/nodes", cinctest.Route{
		Body: `{"containername":"nodes","containerpath":"nodes"}`,
	})
	c := newTestClient(t, srv.Server)
	got, _, err := c.Containers.Get(context.Background(), "nodes")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "nodes" || got.Path != "nodes" {
		t.Errorf("Container = %+v", got)
	}
}

func TestContainers_Create(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/containers", cinctest.Route{
		Status: 201,
		Body:   `{"uri":"http://x/containers/widgets"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]string
			json.Unmarshal(body, &got)
			if got["containername"] != "widgets" || got["containerpath"] != "widgets" {
				t.Errorf("POST body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if _, err := c.Containers.Create(context.Background(), "widgets"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestContainers_Delete(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/containers/widgets", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.Containers.Delete(context.Background(), "widgets"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestContainers_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/containers/missing",
		cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Containers.Get(context.Background(), "missing"); err == nil {
		t.Fatal("expected 404")
	}
}
