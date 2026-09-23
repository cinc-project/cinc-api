package suite

import (
	"context"
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testPushRevisionToTwoGroups pushes the same Policyfile lock to two policy
// groups. The server answers a second PUT of an existing cookbook artifact
// identifier with 409, so the second push only succeeds if PushRevision
// skips artifacts the server already has.
func testPushRevisionToTwoGroups(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	groups := []string{uniqueName(t, "staging"), uniqueName(t, "prod")}
	p := pushPolicy(t, c, groups...)

	if _, _, err := c.CookbookArtifacts.Get(ctx, p.cookbook, p.identifier); err != nil {
		t.Fatalf("CookbookArtifacts.Get: %v", err)
	}
	for _, group := range groups {
		if _, _, err := c.PolicyGroups.GetPolicy(ctx, group, p.name); err != nil {
			t.Errorf("policy %s not associated with %s: %v", p.name, group, err)
		}
	}
}

// testPolicyRevisions reads a pushed policy back through every policy and
// policy-group endpoint, then removes the group association and the revision.
func testPolicyRevisions(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	group := uniqueName(t, "group")
	p := pushPolicy(t, c, group)

	list, _, err := c.Policies.List(ctx)
	if err != nil {
		t.Fatalf("Policies.List: %v", err)
	}
	if _, ok := list[p.name].Revisions[p.revision]; !ok {
		t.Fatalf("policy list entry for %s = %+v, want revision %s", p.name, list[p.name], p.revision)
	}
	revs, _, err := c.Policies.Get(ctx, p.name)
	if err != nil {
		t.Fatalf("Policies.Get: %v", err)
	}
	if _, ok := revs.Revisions[p.revision]; !ok {
		t.Fatalf("revisions of %s = %v, want %s", p.name, revs.Revisions, p.revision)
	}
	rev, _, err := c.Policies.GetRevision(ctx, p.name, p.revision)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if rev.Name != p.name || rev.RevisionID != p.revision || rev.CookbookLocks[p.cookbook].Identifier != p.identifier {
		t.Fatalf("revision = %+v", rev)
	}

	groups, _, err := c.PolicyGroups.List(ctx)
	if err != nil {
		t.Fatalf("PolicyGroups.List: %v", err)
	}
	if groups[group].Policies[p.name].RevisionID != p.revision {
		t.Fatalf("policy group list entry for %s = %+v, want %s at %s", group, groups[group], p.name, p.revision)
	}
	pg, _, err := c.PolicyGroups.Get(ctx, group)
	if err != nil {
		t.Fatalf("PolicyGroups.Get: %v", err)
	}
	if pg.Policies[p.name].RevisionID != p.revision {
		t.Fatalf("policy group %s = %+v, want %s at %s", group, pg, p.name, p.revision)
	}

	if _, err := c.PolicyGroups.DeletePolicy(ctx, group, p.name); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if _, _, err := c.PolicyGroups.GetPolicy(ctx, group, p.name); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("GetPolicy after DeletePolicy: err = %v, want ErrNotFound", err)
	}
	if _, err := c.Policies.DeleteRevision(ctx, p.name, p.revision); err != nil {
		t.Fatalf("DeleteRevision: %v", err)
	}
	if _, _, err := c.Policies.GetRevision(ctx, p.name, p.revision); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("GetRevision after delete: err = %v, want ErrNotFound", err)
	}
}

// pushedPolicy names what pushPolicy created.
type pushedPolicy struct {
	name, revision, cookbook, identifier string
}

// pushPolicy pushes a new policy, locked to one freshly uploaded cookbook
// artifact, to each of groups, and registers the deletion of everything it
// created.
func pushPolicy(t *testing.T, c *cinc.Client, groups ...string) pushedPolicy {
	t.Helper()
	p := pushedPolicy{
		name:       uniqueName(t, "policy"),
		revision:   randomHex(t, 32),
		cookbook:   uniqueName(t, "cookbook"),
		identifier: randomHex(t, 20),
	}
	// Cleanups run last-registered first: groups, then the policy, then the
	// artifact the policy revision refers to.
	cleanup(t, "cookbook artifact "+p.cookbook, func(ctx context.Context) error {
		_, err := c.CookbookArtifacts.Delete(ctx, p.cookbook, p.identifier)
		return err
	})
	cleanup(t, "policy "+p.name, func(ctx context.Context) error {
		_, err := c.Policies.Delete(ctx, p.name)
		return err
	})
	for _, group := range groups {
		cleanup(t, "policy group "+group, func(ctx context.Context) error {
			_, err := c.PolicyGroups.Delete(ctx, group)
			return err
		})
	}

	lockJSON := []byte(`{
		"revision_id":"` + p.revision + `",
		"name":"` + p.name + `",
		"run_list":["recipe[` + p.cookbook + `::default]"],
		"named_run_lists":{},
		"cookbook_locks":{"` + p.cookbook + `":{
			"version":"1.0.0",
			"identifier":"` + p.identifier + `",
			"dotted_decimal_identifier":"1.2.3",
			"cache_key":null,
			"source_options":{"path":"."}
		}},
		"default_attributes":{},
		"override_attributes":{},
		"solution_dependencies":{"Policyfile":[],"dependencies":{}}
	}`)
	cookbooks := map[string]*cinc.LocalCookbook{p.cookbook: localCookbook(t, p.cookbook, "1.0.0", nil)}
	for _, group := range groups {
		if _, _, err := c.Policies.PushRevision(t.Context(), lockJSON, group, cookbooks); err != nil {
			t.Fatalf("PushRevision to %s: %v", group, err)
		}
	}
	return p
}
