package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestGroups_ListAndGet(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/groups",
		cinctest.Route{Body: `{"admins":"http://x/groups/admins","custom":"http://x/groups/custom"}`})
	srv.Handle("GET /organizations/o/groups/admins",
		cinctest.Route{Body: `{
			"name":"admins","groupname":"admins","orgname":"o",
			"actors":["alice"],"users":["alice"],"clients":["node1"],"groups":["billing-admins"]
		}`})

	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	list, _, err := c.Groups.List(ctx)
	if err != nil || list["admins"] == "" {
		t.Fatalf("List: %+v %v", list, err)
	}

	g, _, err := c.Groups.Get(ctx, "admins")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.Name != "admins" || g.GroupName != "admins" || g.OrgName != "o" {
		t.Errorf("Get header fields wrong: %+v", g)
	}
	if !reflect.DeepEqual(g.Users, []string{"alice"}) ||
		!reflect.DeepEqual(g.Clients, []string{"node1"}) ||
		!reflect.DeepEqual(g.Groups, []string{"billing-admins"}) {
		t.Errorf("member fields wrong: %+v", g)
	}
}

func TestGroups_Create(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/groups", cinctest.Route{
		Status: 201,
		Body:   `{"uri":"http://x/groups/devs"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req map[string]any
			json.Unmarshal(body, &req)
			// The Chef Server identifies a new group by "groupname"
			// (or "id"); a "name" key is rejected with a 400.
			if req["groupname"] != "devs" {
				t.Errorf("POST body = %v, want groupname=devs", req)
			}
			if _, ok := req["name"]; ok {
				t.Errorf("POST body should not carry a \"name\" key: %v", req)
			}
		},
	})

	c := newTestClient(t, srv.Server)
	if _, err := c.Groups.Create(context.Background(), "devs"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestGroups_Update_WiresActorsFormat(t *testing.T) {
	// PUT body must wrap users/clients/groups inside an "actors" object — the
	// shape differs from what GET returns. The transformation is the whole
	// point of the Update helper.
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/groups/devs", cinctest.Route{
		Body: `{"name":"devs","groupname":"devs","orgname":"o","actors":["alice"],"users":["alice"],"clients":[],"groups":[]}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req struct {
				GroupName string `json:"groupname"`
				Actors    struct {
					Users   []string `json:"users"`
					Clients []string `json:"clients"`
					Groups  []string `json:"groups"`
				} `json:"actors"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode PUT body: %v (body=%s)", err, body)
			}
			if req.GroupName != "devs" {
				t.Errorf("groupname = %q", req.GroupName)
			}
			sort.Strings(req.Actors.Users)
			if !reflect.DeepEqual(req.Actors.Users, []string{"alice", "bob"}) {
				t.Errorf("actors.users = %v, want [alice bob]", req.Actors.Users)
			}
			if !reflect.DeepEqual(req.Actors.Clients, []string{"node1"}) {
				t.Errorf("actors.clients = %v", req.Actors.Clients)
			}
			if !reflect.DeepEqual(req.Actors.Groups, []string{"billing-admins"}) {
				t.Errorf("actors.groups = %v", req.Actors.Groups)
			}
		},
	})

	c := newTestClient(t, srv.Server)
	g := &Group{
		Name:    "devs",
		Users:   []string{"alice", "bob"},
		Clients: []string{"node1"},
		Groups:  []string{"billing-admins"},
	}
	if _, _, err := c.Groups.Update(context.Background(), g); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestGroups_Update_NilSlicesBecomeEmptyArrays(t *testing.T) {
	// The Chef server is strict: actors.users/clients/groups must be JSON
	// arrays, not null. Nil slices in the Group must serialize as [].
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/groups/empty", cinctest.Route{
		Body: `{}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			// Raw-decode to verify the values are arrays, not null.
			var req struct {
				Actors struct {
					Users   json.RawMessage `json:"users"`
					Clients json.RawMessage `json:"clients"`
					Groups  json.RawMessage `json:"groups"`
				} `json:"actors"`
			}
			json.Unmarshal(body, &req)
			for _, raw := range []json.RawMessage{req.Actors.Users, req.Actors.Clients, req.Actors.Groups} {
				if string(raw) != "[]" {
					t.Errorf("actor list = %s, want []", raw)
				}
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Groups.Update(context.Background(), &Group{Name: "empty"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestGroups_Delete(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/groups/devs", cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)
	if _, err := c.Groups.Delete(context.Background(), "devs"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestGroups_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/groups/missing",
		cinctest.Route{Status: 404, Body: `{"error":["group not found"]}`})
	c := newTestClient(t, srv.Server)
	_, _, err := c.Groups.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected 404")
	}
}
