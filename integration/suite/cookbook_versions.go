package suite

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testCookbookVersions covers the cookbook endpoints that span versions:
// version listing, latest, recipes, the universe, and an environment's
// dependency solving and cookbook listing.
func testCookbookVersions(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	dep := uniqueName(t, "cookbook")
	name := uniqueName(t, "cookbook")
	uploadCookbook(t, c, dep, "1.0.0", nil)
	uploadCookbook(t, c, name, "1.0.0", map[string]string{dep: ">= 1.0.0"})
	uploadCookbook(t, c, name, "2.0.0", map[string]string{dep: ">= 1.0.0"})

	if got := cookbookVersions(t, c, name); !slices.Equal(got, []string{"1.0.0", "2.0.0"}) {
		t.Fatalf("versions = %v, want [1.0.0 2.0.0]", got)
	}

	latest, _, err := c.Cookbooks.ListLatest(ctx)
	if err != nil {
		t.Fatalf("ListLatest: %v", err)
	}
	if url := latest[name]; !strings.HasSuffix(url, "/"+name+"/2.0.0") {
		t.Fatalf("latest %s = %q, want a URL ending in /%s/2.0.0", name, url, name)
	}

	recipes, _, err := c.Cookbooks.ListRecipes(ctx)
	if err != nil {
		t.Fatalf("ListRecipes: %v", err)
	}
	if !slices.Contains(recipes, name) && !slices.Contains(recipes, name+"::default") {
		t.Fatalf("recipes lack %s's default recipe: %v", name, recipes)
	}

	universe, _, err := c.Universe.Get(ctx)
	if err != nil {
		t.Fatalf("Universe.Get: %v", err)
	}
	if got := universe[name]["2.0.0"].Dependencies[dep]; got != ">= 1.0.0" {
		t.Fatalf("universe %s 2.0.0 depends on %s %q, want \">= 1.0.0\"; entry = %+v", name, dep, got, universe[name])
	}

	// An environment pinning name to 1.0.0 solves to that version, pulls in
	// its dependency, and lists only that version.
	env := uniqueName(t, "env")
	cleanup(t, "environment "+env, func(ctx context.Context) error {
		_, err := c.Environments.Delete(ctx, env)
		return err
	})
	if _, err := c.Environments.Create(ctx, &cinc.Environment{Name: env, CookbookVersions: map[string]string{name: "= 1.0.0"}}); err != nil {
		t.Fatalf("create environment: %v", err)
	}
	solved, _, err := c.Environments.CookbookVersions(ctx, env, []string{name})
	if err != nil {
		t.Fatalf("Environments.CookbookVersions: %v", err)
	}
	if solved[name].Version != "1.0.0" {
		t.Fatalf("solved %s = %q, want 1.0.0", name, solved[name].Version)
	}
	if _, ok := solved[dep]; !ok {
		t.Fatalf("solution lacks dependency %s: %v", dep, solved)
	}
	envCookbooks, _, err := c.Environments.ListCookbooks(ctx, env, "all")
	if err != nil {
		t.Fatalf("Environments.ListCookbooks: %v", err)
	}
	var envVersions []string
	for _, v := range envCookbooks[name].Versions {
		envVersions = append(envVersions, v.Version)
	}
	if !slices.Equal(envVersions, []string{"1.0.0"}) {
		t.Fatalf("environment lists %s versions %v, want [1.0.0]", name, envVersions)
	}

	if _, err := c.Cookbooks.Delete(ctx, name, "2.0.0"); err != nil {
		t.Fatalf("Delete 2.0.0: %v", err)
	}
	if got := cookbookVersions(t, c, name); !slices.Equal(got, []string{"1.0.0"}) {
		t.Fatalf("versions after deleting 2.0.0 = %v, want [1.0.0]", got)
	}
	if _, _, err := c.Cookbooks.Get(ctx, name, "2.0.0"); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get 2.0.0 after delete: err = %v, want ErrNotFound", err)
	}
}

// cookbookVersions returns name's versions, sorted.
func cookbookVersions(t *testing.T, c *cinc.Client, name string) []string {
	t.Helper()
	entry, _, err := c.Cookbooks.GetVersions(t.Context(), name, "all")
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}
	var out []string
	for _, v := range entry.Versions {
		out = append(out, v.Version)
	}
	slices.Sort(out)
	return out
}

func testCookbookArtifactListDelete(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name, identifier := uploadArtifact(t, c)

	list, _, err := c.CookbookArtifacts.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !hasIdentifier(list[name], identifier) {
		t.Fatalf("list entry for %s = %+v, want identifier %s", name, list[name], identifier)
	}
	entry, _, err := c.CookbookArtifacts.GetVersions(ctx, name)
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}
	if !hasIdentifier(*entry, identifier) {
		t.Fatalf("GetVersions(%s) = %+v, want identifier %s", name, entry, identifier)
	}

	if _, err := c.CookbookArtifacts.Delete(ctx, name, identifier); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := c.CookbookArtifacts.Get(ctx, name, identifier); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

func hasIdentifier(e cinc.CookbookArtifactListEntry, identifier string) bool {
	for _, v := range e.Versions {
		if v.Identifier == identifier {
			return true
		}
	}
	return false
}
