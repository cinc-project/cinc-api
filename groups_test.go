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
	if _, _, err := c.Groups.Update(context.Background(), "devs", g); err != nil {
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
	if _, _, err := c.Groups.Update(context.Background(), "empty", &Group{}); err != nil {
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

// Chef identifies a group by "groupname"; "name" is a courtesy field not every
// server populates. With Name empty, GroupName is the name to send.
func TestGroups_Update_FallsBackToGroupName(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/groups/admins", cinctest.Route{
		Body: `{"groupname":"admins","actors":{"users":["alice"],"clients":[],"groups":[]}}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			if m := decodeBody(t, body); m["groupname"] != "admins" {
				t.Errorf("body = %s, want groupname=admins", body)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	g := &Group{GroupName: "admins", Users: []string{"alice"}}
	if _, _, err := c.Groups.Update(context.Background(), "admins", g); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

// A PUT whose groupname differs from the path renames the group: erchef
// answers 201 with a Location for the new name (oc_chef_wm_base:
// update_from_json -> handle_rename) and echoes the request body, since
// oc_chef_wm_named_group has no finalize_update_body. Only "groupname" is
// read for the new name (oc_chef_group:update_from_ejson); "name" is not.
func TestGroups_Update_Rename(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/groups/devs", cinctest.Route{
		Status: 201,
		Body:   `{"groupname":"ops","actors":{"users":["alice"],"clients":[],"groups":[]}}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			m := decodeBody(t, body)
			if m["groupname"] != "ops" {
				t.Errorf("body = %s, want groupname=ops", body)
			}
			if _, ok := m["name"]; ok {
				t.Errorf("body = %s, want no name key", body)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	for _, g := range []*Group{
		{Name: "ops", Users: []string{"alice"}},
		{GroupName: "ops", Users: []string{"alice"}},
		{Name: "ops", GroupName: "ops", Users: []string{"alice"}},
	} {
		before := *g
		updated, resp, err := c.Groups.Update(context.Background(), "devs", g)
		if err != nil {
			t.Fatalf("Update(%+v): %v", before, err)
		}
		if resp.StatusCode != http.StatusCreated || updated.Name != "ops" || updated.GroupName != "ops" ||
			!reflect.DeepEqual(updated.Users, []string{"alice"}) {
			t.Errorf("Update(%+v) = %+v (status %d), want ops with alice and 201", before, updated, resp.StatusCode)
		}
		if !reflect.DeepEqual(*g, before) {
			t.Errorf("Update modified the caller's group: %+v, was %+v", *g, before)
		}
	}
}

func TestGroups_Update_Errors(t *testing.T) {
	srv := cinctest.New(t) // no routes: neither call may reach the server
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Groups.Update(context.Background(), "", &Group{Name: "devs"}); err == nil {
		t.Error("Update with no current name returned nil error")
	}
	// A group read back and then given a new Name, but not a new GroupName,
	// is ambiguous: refuse it rather than guess which one is the rename.
	if _, _, err := c.Groups.Update(context.Background(), "devs", &Group{Name: "ops", GroupName: "devs"}); err == nil {
		t.Error("Update with Name and GroupName disagreeing returned nil error")
	}
}
