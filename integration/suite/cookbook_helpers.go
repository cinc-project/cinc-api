package suite

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// uploadCookbook uploads version of cookbook name with a default recipe and
// the given dependencies (cookbook → constraint), and registers its deletion.
func uploadCookbook(t *testing.T, c *cinc.Client, name, version string, deps map[string]string) {
	t.Helper()
	cleanup(t, "cookbook "+name+" "+version, func(ctx context.Context) error {
		_, err := c.Cookbooks.Delete(ctx, name, version)
		return err
	})
	if err := c.Cookbooks.Upload(t.Context(), localCookbook(t, name, version, deps)); err != nil {
		t.Fatalf("upload cookbook %s %s: %v", name, version, err)
	}
}

// uploadArtifact uploads a cookbook artifact with a fresh identifier and
// registers its deletion. It returns the name and identifier.
func uploadArtifact(t *testing.T, c *cinc.Client) (name, identifier string) {
	t.Helper()
	name, identifier = uniqueName(t, "artifact"), randomHex(t, 20)
	cleanup(t, "cookbook artifact "+name, func(ctx context.Context) error {
		_, err := c.CookbookArtifacts.Delete(ctx, name, identifier)
		return err
	})
	if err := c.CookbookArtifacts.Upload(t.Context(), localCookbook(t, name, "1.0.0", nil), identifier); err != nil {
		t.Fatalf("upload cookbook artifact %s: %v", name, err)
	}
	return name, identifier
}

// localCookbook writes a minimal cookbook to a temporary directory and loads
// it. Every file names the cookbook (see uniqueContent).
func localCookbook(t *testing.T, name, version string, deps map[string]string) *cinc.LocalCookbook {
	t.Helper()
	var md strings.Builder
	md.WriteString("name '" + name + "'\nversion '" + version + "'\n")
	depNames := make([]string, 0, len(deps))
	for dep := range deps {
		depNames = append(depNames, dep)
	}
	sort.Strings(depNames)
	for _, dep := range depNames {
		md.WriteString("depends '" + dep + "', '" + deps[dep] + "'\n")
	}
	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "metadata.rb"), md.String())
	writeFile(t, filepath.Join(src, "recipes", "default.rb"), uniqueContent(name+" "+version, "package 'nginx'\n"))
	cb, err := cinc.LocalCookbookFromDir(src, version)
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	return cb
}
