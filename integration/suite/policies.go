package suite

import (
	"context"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testPushRevisionToTwoGroups pushes the same Policyfile lock to two policy
// groups. The server answers a second PUT of an existing cookbook artifact
// identifier with 409, so the second push only succeeds if PushRevision
// skips artifacts the server already has.
func testPushRevisionToTwoGroups(t *testing.T, c *cinc.Client) {
	ctx := t.Context()
	policy := uniqueName(t, "policy")
	cookbook := uniqueName(t, "cookbook")
	identifier := randomHex(t, 20)
	groups := []string{uniqueName(t, "staging"), uniqueName(t, "prod")}
	// Cleanups run last-registered first: groups, then the policy, then the
	// artifact the policy revision refers to.
	cleanup(t, "cookbook artifact "+cookbook, func(ctx context.Context) error {
		_, err := c.CookbookArtifacts.Delete(ctx, cookbook, identifier)
		return err
	})
	cleanup(t, "policy "+policy, func(ctx context.Context) error {
		_, err := c.Policies.Delete(ctx, policy)
		return err
	})
	for _, group := range groups {
		cleanup(t, "policy group "+group, func(ctx context.Context) error {
			_, err := c.PolicyGroups.Delete(ctx, group)
			return err
		})
	}

	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "metadata.rb"), "name '"+cookbook+"'\nversion '1.0.0'\n")
	writeFile(t, filepath.Join(src, "recipes", "default.rb"), "package 'nginx'\n")
	cb, err := cinc.LocalCookbookFromDir(src, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}

	lockJSON := []byte(`{
		"revision_id":"` + randomHex(t, 32) + `",
		"name":"` + policy + `",
		"run_list":["recipe[` + cookbook + `::default]"],
		"named_run_lists":{},
		"cookbook_locks":{"` + cookbook + `":{
			"version":"1.0.0",
			"identifier":"` + identifier + `",
			"dotted_decimal_identifier":"1.2.3",
			"cache_key":null,
			"source_options":{"path":"."}
		}},
		"default_attributes":{},
		"override_attributes":{},
		"solution_dependencies":{"Policyfile":[],"dependencies":{}}
	}`)
	cookbooks := map[string]*cinc.LocalCookbook{cookbook: cb}

	for _, group := range groups {
		if _, _, err := c.Policies.PushRevision(ctx, lockJSON, group, cookbooks); err != nil {
			t.Fatalf("PushRevision to %s: %v", group, err)
		}
	}

	if _, _, err := c.CookbookArtifacts.Get(ctx, cookbook, identifier); err != nil {
		t.Fatalf("CookbookArtifacts.Get: %v", err)
	}
	for _, group := range groups {
		if _, _, err := c.PolicyGroups.GetPolicy(ctx, group, policy); err != nil {
			t.Errorf("policy %s not associated with %s: %v", policy, group, err)
		}
	}
}
