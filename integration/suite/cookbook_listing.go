package suite

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// listingVersions are uploaded in an order that is neither newest-first nor
// string order; listingNewestFirst is the order the client must return.
var (
	listingVersions    = []string{"1.9.0", "10.0.0", "1.10.0", "9.0.0"}
	listingNewestFirst = []string{"10.0.0", "9.0.0", "1.10.0", "1.9.0"}
)

// uploadListingCookbook uploads every listingVersions version of a fresh
// cookbook and returns its name.
func uploadListingCookbook(t *testing.T, c *cinc.Client) string {
	t.Helper()
	name := uniqueName(t, "cookbook")
	for _, v := range listingVersions {
		uploadCookbook(t, c, name, v, nil)
	}
	return name
}

func listedVersions(e cinc.CookbookListEntry) []string {
	out := []string{}
	for _, v := range e.Versions {
		out = append(out, v.Version)
	}
	return out
}

// testCookbookVersionListing checks that every version listing comes back
// newest-first in semantic order, with num_versions applied the same way on
// every endpoint, and that a _latest manifest downloads without a second GET.
func testCookbookVersionListing(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uploadListingCookbook(t, c)

	for _, tc := range []struct {
		numVersions string
		want        []string
	}{
		{"all", listingNewestFirst},
		{"2", listingNewestFirst[:2]},
		{"", listingNewestFirst[:1]},
	} {
		list, _, err := c.Cookbooks.ListVersions(ctx, tc.numVersions)
		if err != nil {
			t.Fatalf("ListVersions(%q): %v", tc.numVersions, err)
		}
		if got := listedVersions(list[name]); !slices.Equal(got, tc.want) {
			t.Errorf("ListVersions(%q) versions = %v, want %v", tc.numVersions, got, tc.want)
		}
	}
	list, _, err := c.Cookbooks.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := listedVersions(list[name]); !slices.Equal(got, listingNewestFirst[:1]) {
		t.Errorf("List versions = %v, want %v", got, listingNewestFirst[:1])
	}

	for _, tc := range []struct {
		numVersions string
		want        []string
	}{
		{"", listingNewestFirst},
		{"all", listingNewestFirst},
		{"3", listingNewestFirst[:3]},
	} {
		entry, _, err := c.Cookbooks.GetVersions(ctx, name, tc.numVersions)
		if err != nil {
			t.Fatalf("GetVersions(%q): %v", tc.numVersions, err)
		}
		if got := listedVersions(*entry); !slices.Equal(got, tc.want) {
			t.Errorf("GetVersions(%q) versions = %v, want %v", tc.numVersions, got, tc.want)
		}
	}

	envList, _, err := c.Environments.ListCookbooks(ctx, "_default", "")
	if err != nil {
		t.Fatalf("Environments.ListCookbooks: %v", err)
	}
	if got := listedVersions(envList[name]); !slices.Equal(got, listingNewestFirst[:1]) {
		t.Errorf("Environments.ListCookbooks versions = %v, want %v", got, listingNewestFirst[:1])
	}
	envOne, _, err := c.Environments.GetCookbook(ctx, "_default", name, "")
	if err != nil {
		t.Fatalf("Environments.GetCookbook: %v", err)
	}
	if got := listedVersions(envOne[name]); !slices.Equal(got, listingNewestFirst) {
		t.Errorf("Environments.GetCookbook versions = %v, want %v", got, listingNewestFirst)
	}

	if _, _, err := c.Cookbooks.ListVersions(ctx, "-1"); err == nil {
		t.Error("ListVersions(\"-1\") succeeded, want an error")
	}

	cb, _, err := c.Cookbooks.Get(ctx, name, cinc.LatestVersion)
	if err != nil {
		t.Fatalf("Get(LatestVersion): %v", err)
	}
	if cb.Version != "10.0.0" {
		t.Fatalf("Get(LatestVersion).Version = %q, want 10.0.0", cb.Version)
	}
	dest := t.TempDir()
	if err := c.Cookbooks.DownloadFiles(ctx, cb, dest); err != nil {
		t.Fatalf("DownloadFiles: %v", err)
	}
	recipe, err := os.ReadFile(filepath.Join(dest, "recipes", "default.rb"))
	if err != nil {
		t.Fatalf("read downloaded recipe: %v", err)
	}
	if !strings.Contains(string(recipe), name+" 10.0.0") {
		t.Errorf("downloaded recipe = %q, want the 10.0.0 content", recipe)
	}
}

// testCookbookListDefaultsToOneVersion: erchef's GET /cookbooks with no
// num_versions lists one version per cookbook (chef_wm_util:num_versions/1
// defaults to 1). The client relies on that default for Cookbooks.List.
func testCookbookListDefaultsToOneVersion(t *testing.T, tgt Target, c *cinc.Client) {
	name := uploadListingCookbook(t, c)
	status, body := rawRequest(t, tgt, http.MethodGet, "/organizations/"+tgt.Org+"/cookbooks", "1", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /cookbooks: status %d (%s)", status, body)
	}
	var list map[string]cinc.CookbookListEntry
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode GET /cookbooks: %v", err)
	}
	if got := listedVersions(list[name]); !slices.Equal(got, listingNewestFirst[:1]) {
		t.Fatalf("GET /cookbooks lists %s versions %v, want only %v", name, got, listingNewestFirst[:1])
	}
}

// testRejectsInvalidNumVersions: erchef answers a num_versions that is
// neither "all" nor a non-negative integer with a 400
// (chef_wm_util:parse_number throws invalid_num_versions).
func testRejectsInvalidNumVersions(t *testing.T, tgt Target, _ *cinc.Client) {
	for _, nv := range []string{"-1", "abc"} {
		status, body := rawRequest(t, tgt, http.MethodGet, "/organizations/"+tgt.Org+"/cookbooks?num_versions="+nv, "1", nil)
		if status != http.StatusBadRequest {
			t.Errorf("GET /cookbooks?num_versions=%s: status %d (%s), want 400", nv, status, body)
		}
	}
}
