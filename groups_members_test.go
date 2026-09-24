package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// A group file written in the server's PUT shape nests its members under an
// "actors" object. Decoding it must not lose them: Groups.Update would then
// PUT an empty group.
func TestGroupUnmarshal_NestedActorsObject(t *testing.T) {
	var g Group
	err := json.Unmarshal([]byte(`{"groupname":"devs","actors":{"users":["alice"],"clients":["node1"],"groups":["ops"]}}`), &g)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := Group{GroupName: "devs", Users: []string{"alice"}, Clients: []string{"node1"}, Groups: []string{"ops"}}
	if !reflect.DeepEqual(g, want) {
		t.Errorf("got %+v, want %+v", g, want)
	}
}

// A GET response carries "actors" as a flat list of names (clients ++ users on
// erchef) next to the typed lists. It cannot be split back by kind, and the
// typed lists already say everything it does, so it is ignored.
func TestGroupUnmarshal_FlatActorsListIgnored(t *testing.T) {
	var g Group
	err := json.Unmarshal([]byte(`{"name":"devs","groupname":"devs","orgname":"o",
		"actors":["node1","alice"],"users":["alice"],"clients":["node1"],"groups":[]}`), &g)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := Group{Name: "devs", GroupName: "devs", OrgName: "o",
		Users: []string{"alice"}, Clients: []string{"node1"}, Groups: []string{}}
	if !reflect.DeepEqual(g, want) {
		t.Errorf("got %+v, want %+v", g, want)
	}
}

// Both shapes at once merge, without duplicating a name listed in each.
func TestGroupUnmarshal_MergesBothShapes(t *testing.T) {
	var g Group
	err := json.Unmarshal([]byte(`{"groupname":"devs","users":["alice"],
		"actors":{"users":["alice","bob"],"clients":["node1"]}}`), &g)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(g.Users, []string{"alice", "bob"}) || !reflect.DeepEqual(g.Clients, []string{"node1"}) || g.Groups != nil {
		t.Errorf("got %+v", g)
	}
}

func TestGroupUnmarshal_NullActorsAndErrors(t *testing.T) {
	var g Group
	if err := json.Unmarshal([]byte(`{"groupname":"devs","actors":null}`), &g); err != nil || g.GroupName != "devs" {
		t.Errorf("null actors: %+v %v", g, err)
	}
	for _, bad := range []string{
		`{"groupname":7}`,
		`{"actors":{"users":"alice"}}`,
	} {
		if err := json.Unmarshal([]byte(bad), &Group{}); err == nil {
			t.Errorf("Unmarshal(%s) = nil error, want one", bad)
		}
	}
}

// erchef answers a group PUT by echoing the request body, which is in the
// nested shape; Update must still return the members.
func TestGroups_Update_DecodesEchoedPutBody(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/groups/devs", cinctest.Route{
		Body: `{"groupname":"devs","actors":{"users":["alice"],"clients":[],"groups":[]}}`,
	})
	c := newTestClient(t, srv.Server)
	got, _, err := c.Groups.Update(context.Background(), &Group{Name: "devs", Users: []string{"alice"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(got.Users, []string{"alice"}) {
		t.Errorf("Update returned %+v, want users [alice]", got)
	}
}

func TestParseMemberKind(t *testing.T) {
	for in, want := range map[string]MemberKind{
		"user": MemberUser, "users": MemberUser,
		"client": MemberClient, "clients": MemberClient,
		"group": MemberGroup, "groups": MemberGroup,
	} {
		got, err := ParseMemberKind(in)
		if err != nil || got != want {
			t.Errorf("ParseMemberKind(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "actor", "User", "userz"} {
		if _, err := ParseMemberKind(bad); err == nil {
			t.Errorf("ParseMemberKind(%q) = nil error, want one", bad)
		}
	}
}

// fakeGroup serves one group the way erchef does: GET returns the stored
// members, PUT echoes the request body and keeps only the members named in
// known (erchef silently drops a name it cannot resolve). It counts the PUTs.
type fakeGroup struct {
	users, clients, groups []string
	known                  []string
	puts                   int
	// keep, if set, lists names a PUT cannot remove (a server that ignores
	// the removal, or a concurrent re-add).
	keep []string
}

func (f *fakeGroup) serve(t *testing.T, srv *cinctest.Server, name string) {
	path := "/organizations/o/groups/" + name
	f.publish(t, srv, path)
	srv.Handle("PUT "+path, cinctest.Route{
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			f.puts++
			var req struct {
				Actors struct{ Users, Clients, Groups []string } `json:"actors"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode PUT: %v", err)
			}
			f.users = f.resolve(f.users, req.Actors.Users)
			f.clients = f.resolve(f.clients, req.Actors.Clients)
			f.groups = f.resolve(f.groups, req.Actors.Groups)
			f.publish(t, srv, path)
		},
		Body: `{}`,
	})
}

// resolve is the membership a PUT of sent leaves, given what was there.
func (f *fakeGroup) resolve(before, sent []string) []string {
	out := []string{}
	for _, n := range sent {
		if slices.Contains(f.known, n) {
			out = append(out, n)
		}
	}
	for _, n := range f.keep {
		if slices.Contains(before, n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func (f *fakeGroup) publish(t *testing.T, srv *cinctest.Server, path string) {
	body, err := json.Marshal(map[string]any{
		"groupname": "devs", "orgname": "o",
		"actors": append(slices.Clone(f.clients), f.users...),
		"users":  nonNil(f.users), "clients": nonNil(f.clients), "groups": nonNil(f.groups),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle("GET "+path, cinctest.Route{Body: string(body)})
}

func TestGroups_AddMembers(t *testing.T) {
	srv := cinctest.New(t)
	f := &fakeGroup{users: []string{"alice"}, clients: []string{"node1"}, known: []string{"alice", "bob", "carol", "node1"}}
	f.serve(t, srv, "devs")
	c := newTestClient(t, srv.Server)

	got, err := c.Groups.AddMembers(context.Background(), "devs", MemberUser, "bob", "alice", "ghost", "bob", "carol")
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	want := &MemberChange{Changed: []string{"bob", "carol"}, Unchanged: []string{"alice"}, Dropped: []string{"ghost"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AddMembers = %+v, want %+v", got, want)
	}
	if f.puts != 1 {
		t.Errorf("PUTs = %d, want 1", f.puts)
	}
	// The other kinds ride along untouched.
	if !reflect.DeepEqual(f.clients, []string{"node1"}) || !reflect.DeepEqual(f.users, []string{"alice", "bob", "carol"}) {
		t.Errorf("group after add: users %v clients %v", f.users, f.clients)
	}
}

func TestGroups_AddMembers_NothingToDoSkipsPut(t *testing.T) {
	srv := cinctest.New(t)
	f := &fakeGroup{clients: []string{"node1"}, known: []string{"node1"}}
	f.serve(t, srv, "devs")
	c := newTestClient(t, srv.Server)

	got, err := c.Groups.AddMembers(context.Background(), "devs", MemberClient, "node1")
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if !reflect.DeepEqual(got, &MemberChange{Unchanged: []string{"node1"}}) {
		t.Errorf("AddMembers = %+v", got)
	}
	if f.puts != 0 {
		t.Errorf("PUTs = %d, want 0", f.puts)
	}
}

func TestGroups_RemoveMembers(t *testing.T) {
	srv := cinctest.New(t)
	f := &fakeGroup{groups: []string{"ops", "qa", "sec"}, known: []string{"ops", "qa", "sec"}, keep: []string{"sec"}}
	f.serve(t, srv, "devs")
	c := newTestClient(t, srv.Server)

	got, err := c.Groups.RemoveMembers(context.Background(), "devs", MemberGroup, "qa", "nope", "sec", "qa")
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	want := &MemberChange{Changed: []string{"qa"}, Unchanged: []string{"nope"}, Dropped: []string{"sec"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RemoveMembers = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(f.groups, []string{"ops", "sec"}) {
		t.Errorf("groups after remove = %v", f.groups)
	}
}

func TestGroups_RemoveMembers_NothingToDoSkipsPut(t *testing.T) {
	srv := cinctest.New(t)
	f := &fakeGroup{}
	f.serve(t, srv, "devs")
	c := newTestClient(t, srv.Server)

	got, err := c.Groups.RemoveMembers(context.Background(), "devs", MemberUser, "alice")
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	if !reflect.DeepEqual(got, &MemberChange{Unchanged: []string{"alice"}}) || f.puts != 0 {
		t.Errorf("RemoveMembers = %+v, PUTs = %d", got, f.puts)
	}
}

// Bad arguments are rejected before any request: the harness fails a test
// on any request, since no route is registered.
func TestGroups_MemberChange_ValidatesFirst(t *testing.T) {
	srv := cinctest.New(t)
	c := newTestClient(t, srv.Server)
	ctx := context.Background()
	for name, call := range map[string]func() (*MemberChange, error){
		"bad kind":   func() (*MemberChange, error) { return c.Groups.AddMembers(ctx, "devs", "actor", "alice") },
		"no group":   func() (*MemberChange, error) { return c.Groups.RemoveMembers(ctx, "", MemberUser, "alice") },
		"empty name": func() (*MemberChange, error) { return c.Groups.AddMembers(ctx, "devs", MemberUser, "alice", "") },
	} {
		if got, err := call(); err == nil {
			t.Errorf("%s: got %+v, want an error", name, got)
		}
	}
	got, err := c.Groups.AddMembers(ctx, "devs", MemberUser)
	if err != nil || !reflect.DeepEqual(got, &MemberChange{}) {
		t.Errorf("no names: %+v %v, want an empty change and no request", got, err)
	}
}

func TestGroups_MemberChange_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("get", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/groups/devs", cinctest.Route{Status: 404, Body: `{"error":["Cannot load group devs"]}`})
		c := newTestClient(t, srv.Server)
		if _, err := c.Groups.AddMembers(ctx, "devs", MemberUser, "alice"); err == nil {
			t.Fatal("want the GET's error")
		}
	})

	t.Run("put", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/groups/devs", cinctest.Route{Body: `{"groupname":"devs"}`})
		srv.Handle("PUT /organizations/o/groups/devs", cinctest.Route{Status: 403, Body: `{"error":["no"]}`})
		c := newTestClient(t, srv.Server)
		if _, err := c.Groups.AddMembers(ctx, "devs", MemberUser, "alice"); err == nil {
			t.Fatal("want the PUT's error")
		}
	})

	t.Run("read back", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/groups/devs", cinctest.Route{Body: `{"groupname":"devs"}`})
		srv.Handle("PUT /organizations/o/groups/devs", cinctest.Route{
			Body: `{}`,
			Assert: func(*testing.T, *http.Request, []byte) {
				srv.Handle("GET /organizations/o/groups/devs", cinctest.Route{Status: 403, Body: `{"error":["no"]}`})
			},
		})
		c := newTestClient(t, srv.Server)
		_, err := c.Groups.AddMembers(ctx, "devs", MemberUser, "alice")
		if err == nil || !strings.Contains(err.Error(), "reading it back") {
			t.Fatalf("err = %v, want one saying the update landed but the read-back failed", err)
		}
	})
}
