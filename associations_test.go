package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestAssociations_ListMembers(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/users", cinctest.Route{
		Body: `[{"user":{"username":"paperlatte"}},{"user":{"username":"grantmc"}}]`,
	})
	c := newTestClient(t, srv.Server)

	members, _, err := c.Associations.ListMembers(context.Background())
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 || members[0] != "paperlatte" || members[1] != "grantmc" {
		t.Fatalf("ListMembers = %v", members)
	}
}

func TestAssociations_GetMember(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/users/paperlatte", cinctest.Route{
		Body: `{"username":"paperlatte","email":"latte@x","display_name":"Ms. Latte"}`,
	})
	c := newTestClient(t, srv.Server)

	u, _, err := c.Associations.GetMember(context.Background(), "paperlatte")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if u.Username != "paperlatte" || u.Email != "latte@x" || u.DisplayName != "Ms. Latte" {
		t.Fatalf("GetMember = %+v", u)
	}
}

func TestAssociations_AddMember(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/users", cinctest.Route{
		Status: 201,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]string
			json.Unmarshal(body, &got)
			if got["username"] != "paperlatte" {
				t.Errorf("POST body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	if _, err := c.Associations.AddMember(context.Background(), "paperlatte"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
}

func TestAssociations_RemoveMember(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/users/paperlatte", cinctest.Route{
		Body: `{"username":"paperlatte","email":"latte@x"}`,
	})
	c := newTestClient(t, srv.Server)

	u, _, err := c.Associations.RemoveMember(context.Background(), "paperlatte")
	if err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if u.Username != "paperlatte" {
		t.Fatalf("RemoveMember = %+v", u)
	}
}

func TestAssociations_ListInvites(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/association_requests", cinctest.Route{
		Body: `[{"id":"abc","username":"marygupta"},{"id":"def","username":"johnirving"}]`,
	})
	c := newTestClient(t, srv.Server)

	invites, _, err := c.Associations.ListInvites(context.Background())
	if err != nil {
		t.Fatalf("ListInvites: %v", err)
	}
	if len(invites) != 2 || invites[0].ID != "abc" || invites[0].Username != "marygupta" {
		t.Fatalf("ListInvites = %+v", invites)
	}
}

func TestAssociations_Invite(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/association_requests", cinctest.Route{
		Status: 201,
		Body:   `{"uri":"https://chef/organizations/o/association_requests/abc"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]string
			json.Unmarshal(body, &got)
			if got["user"] != "billysmith" {
				t.Errorf("POST body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	res, _, err := c.Associations.Invite(context.Background(), "billysmith")
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if res.URI == "" {
		t.Fatalf("Invite = %+v", res)
	}
}

func TestAssociations_RescindInvite(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/association_requests/abc", cinctest.Route{
		Body: `{"id":"abc","orgname":"o","username":"janedoe"}`,
	})
	c := newTestClient(t, srv.Server)

	if _, err := c.Associations.RescindInvite(context.Background(), "abc"); err != nil {
		t.Fatalf("RescindInvite: %v", err)
	}
}

func TestAssociations_ListUserInvites(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/janedoe/association_requests", cinctest.Route{
		Body: `[{"id":"abc","orgname":"testorg"}]`,
	})
	c := newTestClient(t, srv.Server)

	invites, _, err := c.Associations.ListUserInvites(context.Background(), "janedoe")
	if err != nil {
		t.Fatalf("ListUserInvites: %v", err)
	}
	if len(invites) != 1 || invites[0].ID != "abc" || invites[0].OrgName != "testorg" {
		t.Fatalf("ListUserInvites = %+v", invites)
	}
}

func TestAssociations_UserInviteCount(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/janedoe/association_requests/count", cinctest.Route{
		Body: `{"value":3}`,
	})
	c := newTestClient(t, srv.Server)

	n, _, err := c.Associations.UserInviteCount(context.Background(), "janedoe")
	if err != nil {
		t.Fatalf("UserInviteCount: %v", err)
	}
	if n != 3 {
		t.Fatalf("UserInviteCount = %d", n)
	}
}

func TestAssociations_RespondInvite_Accept(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /users/janedoe/association_requests/abc", cinctest.Route{
		Body: `{"id":"abc","orgname":"testorg","response":"accept"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]string
			json.Unmarshal(body, &got)
			if got["response"] != "accept" {
				t.Errorf("PUT body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	if _, err := c.Associations.RespondInvite(context.Background(), "janedoe", "abc", true); err != nil {
		t.Fatalf("RespondInvite: %v", err)
	}
}

func TestAssociations_RespondInvite_Reject(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /users/janedoe/association_requests/abc", cinctest.Route{
		Body: `{"id":"abc","orgname":"testorg","response":"reject"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var got map[string]string
			json.Unmarshal(body, &got)
			if got["response"] != "reject" {
				t.Errorf("PUT body = %v", got)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	if _, err := c.Associations.RespondInvite(context.Background(), "janedoe", "abc", false); err != nil {
		t.Fatalf("RespondInvite: %v", err)
	}
}

func TestAssociations_ListMembers_Error(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/users",
		cinctest.Route{Status: 403, Body: `{"error":["forbidden"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Associations.ListMembers(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestAssociations_ListUserOrgs_Error(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/janedoe/organizations",
		cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Associations.ListUserOrgs(context.Background(), "janedoe"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAssociations_ListUserOrgs(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /users/janedoe/organizations", cinctest.Route{
		Body: `[{"organization":{"name":"testorg","full_name":"Test Org","guid":"g1"}}]`,
	})
	c := newTestClient(t, srv.Server)

	orgs, _, err := c.Associations.ListUserOrgs(context.Background(), "janedoe")
	if err != nil {
		t.Fatalf("ListUserOrgs: %v", err)
	}
	if len(orgs) != 1 || orgs[0].Name != "testorg" || orgs[0].FullName != "Test Org" {
		t.Fatalf("ListUserOrgs = %+v", orgs)
	}
}
