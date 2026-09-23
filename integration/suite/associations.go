package suite

import (
	"slices"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testInviteAccept invites a new user, who then sees the invitation and
// accepts it, becoming a member; the admin then removes them.
func testInviteAccept(t *testing.T, tgt Target, c *cinc.Client) {
	ctx := t.Context()
	user, key := newUser(t, c)
	uc := clientAs(t, tgt, user, key)
	id := invite(t, c, user)

	userInvites, _, err := uc.Associations.ListUserInvites(ctx, user)
	if err != nil {
		t.Fatalf("ListUserInvites: %v", err)
	}
	if !slices.ContainsFunc(userInvites, func(i cinc.Invitation) bool { return i.ID == id && i.OrgName == tgt.Org }) {
		t.Fatalf("user-side invites = %+v, want %s from %s", userInvites, id, tgt.Org)
	}
	if n, _, err := uc.Associations.UserInviteCount(ctx, user); err != nil || n < 1 {
		t.Fatalf("UserInviteCount = %d, %v; want at least 1", n, err)
	}

	if _, err := uc.Associations.RespondInvite(ctx, user, id, true); err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	if !isMember(t, c, user) {
		t.Fatalf("%s is not a member after accepting", user)
	}
	member, _, err := c.Associations.GetMember(ctx, user)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if member.Username != user {
		t.Fatalf("member = %+v", member)
	}
	orgs, _, err := uc.Associations.ListUserOrgs(ctx, user)
	if err != nil {
		t.Fatalf("ListUserOrgs: %v", err)
	}
	if !slices.ContainsFunc(orgs, func(o cinc.Org) bool { return o.Name == tgt.Org }) {
		t.Fatalf("%s's orgs = %+v, want %s", user, orgs, tgt.Org)
	}

	if _, _, err := c.Associations.RemoveMember(ctx, user); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if isMember(t, c, user) {
		t.Fatalf("%s is still a member after RemoveMember", user)
	}
}

// testInviteReject: a rejected invitation disappears and adds no member.
func testInviteReject(t *testing.T, tgt Target, c *cinc.Client) {
	ctx := t.Context()
	user, key := newUser(t, c)
	id := invite(t, c, user)

	if _, err := clientAs(t, tgt, user, key).Associations.RespondInvite(ctx, user, id, false); err != nil {
		t.Fatalf("reject invite: %v", err)
	}
	if isMember(t, c, user) {
		t.Fatalf("%s became a member by rejecting", user)
	}
	if hasInvite(t, c, id) {
		t.Fatalf("invite %s still listed after rejection", id)
	}
}

// testInviteRescind: the org can withdraw an invitation before it is answered.
func testInviteRescind(t *testing.T, _ Target, c *cinc.Client) {
	user, _ := newUser(t, c)
	id := invite(t, c, user)
	if _, err := c.Associations.RescindInvite(t.Context(), id); err != nil {
		t.Fatalf("RescindInvite: %v", err)
	}
	if hasInvite(t, c, id) {
		t.Fatalf("invite %s still listed after rescinding", id)
	}
}

// testAddMember associates a user directly, without an invitation.
func testAddMember(t *testing.T, _ Target, c *cinc.Client) {
	user, _ := newUser(t, c)
	if _, err := c.Associations.AddMember(t.Context(), user); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if !isMember(t, c, user) {
		t.Fatalf("%s is not a member after AddMember", user)
	}
	if _, _, err := c.Associations.RemoveMember(t.Context(), user); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
}

// invite invites user to the org and returns the invitation's id, as the
// org-side listing reports it.
func invite(t *testing.T, c *cinc.Client, user string) string {
	t.Helper()
	if _, _, err := c.Associations.Invite(t.Context(), user); err != nil {
		t.Fatalf("Invite %s: %v", user, err)
	}
	invites, _, err := c.Associations.ListInvites(t.Context())
	if err != nil {
		t.Fatalf("ListInvites: %v", err)
	}
	for _, i := range invites {
		if i.Username == user {
			return i.ID
		}
	}
	t.Fatalf("invite for %s missing from %+v", user, invites)
	return ""
}

func hasInvite(t *testing.T, c *cinc.Client, id string) bool {
	t.Helper()
	invites, _, err := c.Associations.ListInvites(t.Context())
	if err != nil {
		t.Fatalf("ListInvites: %v", err)
	}
	return slices.ContainsFunc(invites, func(i cinc.Invitation) bool { return i.ID == id })
}

func isMember(t *testing.T, c *cinc.Client, user string) bool {
	t.Helper()
	members, _, err := c.Associations.ListMembers(t.Context())
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	return slices.Contains(members, user)
}
