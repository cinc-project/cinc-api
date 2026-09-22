package integration

import (
	"context"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestIntegration_PushRevisionToTwoGroups pushes the same Policyfile lock to
// two policy groups. The server answers a second PUT of an existing cookbook
// artifact identifier with 409, so the second push only succeeds if
// PushRevision skips artifacts the server already has.
func TestIntegration_PushRevisionToTwoGroups(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	src := filepath.Join(t.TempDir(), "nginx")
	writeFile(t, filepath.Join(src, "metadata.rb"), "name 'nginx'\nversion '1.0.0'\n")
	writeFile(t, filepath.Join(src, "recipes", "default.rb"), "package 'nginx'\n")
	cb, err := cinc.LocalCookbookFromDir(src, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}

	const identifier = "fedcba9876543210fedcba9876543210fedcba98"
	lockJSON := []byte(`{
		"revision_id":"1111111111111111111111111111111111111111111111111111111111111111",
		"name":"web",
		"run_list":["recipe[nginx::default]"],
		"named_run_lists":{},
		"cookbook_locks":{"nginx":{
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
	cookbooks := map[string]*cinc.LocalCookbook{"nginx": cb}

	for _, group := range []string{"staging", "prod"} {
		if _, _, err := c.Policies.PushRevision(ctx, lockJSON, group, cookbooks); err != nil {
			t.Fatalf("PushRevision to %s: %v", group, err)
		}
	}

	if _, _, err := c.CookbookArtifacts.Get(ctx, "nginx", identifier); err != nil {
		t.Fatalf("CookbookArtifacts.Get: %v", err)
	}
	for _, group := range []string{"staging", "prod"} {
		if _, _, err := c.PolicyGroups.GetPolicy(ctx, group, "web"); err != nil {
			t.Errorf("policy web not associated with %s: %v", group, err)
		}
	}
}
