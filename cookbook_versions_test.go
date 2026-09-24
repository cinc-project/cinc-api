package cinc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestCompareCookbookVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2", "1.2.0", 0},
		{"01.02.03", "1.2.3", 0},
		{"10.0.0", "9.0.0", 1},
		{"9.0.0", "10.0.0", -1},
		{"1.10.0", "1.9.0", 1},
		{"1.0.10", "1.0.9", 1},
		{"2.0", "1.99.99", 1},
		// Unparseable versions sort below every valid one, and among
		// themselves by string, so the order is total and deterministic.
		{"1.0.0-rc1", "0.0.1", -1},
		{"0.0.1", "1.0.0-rc1", 1},
		{"abc", "abd", -1},
		{"abc", "abc", 0},
		{"", "0.0.0", -1},
		{"99999999999999999999.0.0", "1.0.0", -1}, // overflows: unparseable
	}
	for _, tc := range cases {
		if got := CompareCookbookVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareCookbookVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// versionList renders a list of versions as a JSON "versions" array.
func versionList(vs ...string) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = fmt.Sprintf(`{"url":"http://x/%s","version":%q}`, v, v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func versionsOf(e CookbookListEntry) []string {
	out := make([]string, len(e.Versions))
	for i, v := range e.Versions {
		out[i] = v.Version
	}
	return out
}

// scrambled is the order the fake server sends versions in: neither
// newest-first nor string order, so only a semantic sort yields want.
var (
	scrambled    = versionList("1.9.0", "10.0.0", "1.10.0", "9.0.0")
	newestFirst  = []string{"10.0.0", "9.0.0", "1.10.0", "1.9.0"}
	scrambledOne = `{"nginx":{"url":"http://x/nginx","versions":` + scrambled + `}}`
)

func TestCookbooks_ListVersions(t *testing.T) {
	cases := []struct {
		numVersions string
		wantQuery   string // "" means absent
		want        []string
	}{
		{"all", "all", newestFirst},
		{"2", "2", newestFirst[:2]},
		{"0", "0", []string{}},
		{"10", "10", newestFirst},
		// erchef's default for GET /cookbooks is one version per cookbook;
		// the client trims to it for a server that sends more.
		{"", "", newestFirst[:1]},
	}
	for _, tc := range cases {
		t.Run("num_versions="+tc.numVersions, func(t *testing.T) {
			srv := cinctest.New(t)
			var gotQuery string
			var had bool
			srv.Handle("GET /organizations/o/cookbooks", cinctest.Route{
				Body: `{"nginx":{"url":"http://x/nginx","versions":` + scrambled + `},` +
					`"apt":{"url":"http://x/apt","versions":[]}}`,
				Assert: func(t *testing.T, r *http.Request, _ []byte) {
					gotQuery = r.URL.Query().Get("num_versions")
					_, had = r.URL.Query()["num_versions"]
				},
			})
			c := newTestClient(t, srv.Server)

			list, _, err := c.Cookbooks.ListVersions(context.Background(), tc.numVersions)
			if err != nil {
				t.Fatalf("ListVersions: %v", err)
			}
			if tc.wantQuery == "" && had {
				t.Errorf("num_versions = %q, want it absent", gotQuery)
			}
			if gotQuery != tc.wantQuery {
				t.Errorf("num_versions = %q, want %q", gotQuery, tc.wantQuery)
			}
			if got := versionsOf(list["nginx"]); !slices.Equal(got, tc.want) {
				t.Errorf("versions = %v, want %v", got, tc.want)
			}
			if len(list["apt"].Versions) != 0 {
				t.Errorf("apt versions = %v, want none", list["apt"].Versions)
			}
		})
	}
}

func TestCookbooks_ListIsLatestOnly(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks", cinctest.Route{
		Body: scrambledOne,
		Assert: func(t *testing.T, r *http.Request, _ []byte) {
			if r.URL.RawQuery != "" {
				t.Errorf("query = %q, want none", r.URL.RawQuery)
			}
		},
	})
	c := newTestClient(t, srv.Server)

	list, _, err := c.Cookbooks.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := versionsOf(list["nginx"]); !slices.Equal(got, []string{"10.0.0"}) {
		t.Fatalf("versions = %v, want [10.0.0]", got)
	}
}

func TestCookbooks_GetVersionsSortsAndTrims(t *testing.T) {
	// erchef ignores num_versions on GET /cookbooks/NAME and always sends
	// every version, so the client applies the limit itself.
	for numVersions, want := range map[string][]string{
		"":    newestFirst,
		"all": newestFirst,
		"1":   newestFirst[:1],
		"3":   newestFirst[:3],
	} {
		t.Run("num_versions="+numVersions, func(t *testing.T) {
			srv := cinctest.New(t)
			srv.Handle("GET /organizations/o/cookbooks/nginx", cinctest.Route{Body: scrambledOne})
			c := newTestClient(t, srv.Server)

			entry, _, err := c.Cookbooks.GetVersions(context.Background(), "nginx", numVersions)
			if err != nil {
				t.Fatalf("GetVersions: %v", err)
			}
			if got := versionsOf(*entry); !slices.Equal(got, want) {
				t.Fatalf("versions = %v, want %v", got, want)
			}
		})
	}
}

func TestEnvironments_CookbookListsSortAndTrim(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/environments/prod/cookbooks", cinctest.Route{Body: scrambledOne})
	srv.Handle("GET /organizations/o/environments/prod/cookbooks/nginx", cinctest.Route{Body: scrambledOne})
	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	// erchef defaults to one version when listing every cookbook in an
	// environment, and to all of them for a single cookbook.
	for _, tc := range []struct {
		name string
		get  func(string) (map[string]CookbookListEntry, *Response, error)
		nv   string
		want []string
	}{
		{"ListCookbooks default", func(nv string) (map[string]CookbookListEntry, *Response, error) {
			return c.Environments.ListCookbooks(ctx, "prod", nv)
		}, "", newestFirst[:1]},
		{"ListCookbooks all", func(nv string) (map[string]CookbookListEntry, *Response, error) {
			return c.Environments.ListCookbooks(ctx, "prod", nv)
		}, "all", newestFirst},
		{"GetCookbook default", func(nv string) (map[string]CookbookListEntry, *Response, error) {
			return c.Environments.GetCookbook(ctx, "prod", "nginx", nv)
		}, "", newestFirst},
		{"GetCookbook 2", func(nv string) (map[string]CookbookListEntry, *Response, error) {
			return c.Environments.GetCookbook(ctx, "prod", "nginx", nv)
		}, "2", newestFirst[:2]},
	} {
		m, _, err := tc.get(tc.nv)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := versionsOf(m["nginx"]); !slices.Equal(got, tc.want) {
			t.Errorf("%s: versions = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNumVersionsRejectedBeforeRequest(t *testing.T) {
	srv := cinctest.New(t) // no routes: any request fails the test
	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	for _, nv := range []string{"-1", "abc", "1.5", " 1", "ALL", "+1"} {
		calls := map[string]func() error{
			"ListVersions": func() error { _, _, err := c.Cookbooks.ListVersions(ctx, nv); return err },
			"GetVersions":  func() error { _, _, err := c.Cookbooks.GetVersions(ctx, "nginx", nv); return err },
			"ListCookbooks": func() error {
				_, _, err := c.Environments.ListCookbooks(ctx, "prod", nv)
				return err
			},
			"GetCookbook": func() error {
				_, _, err := c.Environments.GetCookbook(ctx, "prod", "nginx", nv)
				return err
			},
		}
		for name, call := range calls {
			err := call()
			if err == nil || !strings.Contains(err.Error(), "num_versions") {
				t.Errorf("%s(%q): err = %v, want a num_versions error", name, nv, err)
			}
		}
	}
}

func TestCookbookListErrorsPropagate(t *testing.T) {
	srv := cinctest.New(t)
	for _, p := range []string{"/cookbooks", "/environments/prod/cookbooks", "/environments/prod/cookbooks/nginx"} {
		srv.Handle("GET /organizations/o"+p, cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	}
	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	if m, _, err := c.Cookbooks.ListVersions(ctx, "all"); err == nil || m != nil {
		t.Errorf("ListVersions = %v, %v; want nil and an error", m, err)
	}
	if m, _, err := c.Environments.ListCookbooks(ctx, "prod", ""); err == nil || m != nil {
		t.Errorf("ListCookbooks = %v, %v; want nil and an error", m, err)
	}
	if m, _, err := c.Environments.GetCookbook(ctx, "prod", "nginx", ""); err == nil || m != nil {
		t.Errorf("GetCookbook = %v, %v; want nil and an error", m, err)
	}
}

func TestCookbooks_LatestVersionSentinel(t *testing.T) {
	if LatestVersion != "_latest" {
		t.Fatalf("LatestVersion = %q", LatestVersion)
	}
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/nginx/_latest",
		cinctest.Route{Body: `{"cookbook_name":"nginx","version":"10.0.0","name":"nginx-10.0.0"}`})
	c := newTestClient(t, srv.Server)

	cb, _, err := c.Cookbooks.Get(context.Background(), "nginx", LatestVersion)
	if err != nil || cb.Version != "10.0.0" {
		t.Fatalf("Get(LatestVersion) = %+v, %v", cb, err)
	}
}

// DownloadFiles writes an already-fetched manifest's files without asking
// the server for the manifest again: only the bookshelf is contacted.
func TestCookbooks_DownloadFiles(t *testing.T) {
	srv := cinctest.New(t)
	var apiCalls int
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/recipes/default.rb" {
			if r.Header.Get("X-Ops-Authorization-1") != "" {
				t.Errorf("bookshelf GET carried a Chef signing header")
			}
			w.Write([]byte("package 'nginx'\n"))
			return
		}
		apiCalls++
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestClient(t, srv.Server)
	cb := &Cookbook{CookbookName: "nginx", Version: "10.0.0", AllFilesManifest: []CookbookFileRef{{
		Name: "recipes/default.rb", Path: "recipes/default.rb",
		Checksum: md5Hex([]byte("package 'nginx'\n")), URL: srv.Server.URL + "/files/recipes/default.rb",
	}}}
	dest := t.TempDir()

	if err := c.Cookbooks.DownloadFiles(context.Background(), cb, dest); err != nil {
		t.Fatalf("DownloadFiles: %v", err)
	}
	if apiCalls != 0 {
		t.Errorf("DownloadFiles made %d API requests, want 0", apiCalls)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "recipes", "default.rb")); err != nil || string(data) != "package 'nginx'\n" {
		t.Fatalf("recipe: %q, %v", data, err)
	}

	if err := c.Cookbooks.DownloadFiles(context.Background(), nil, dest); err == nil {
		t.Error("DownloadFiles(nil) succeeded, want an error")
	}
}

func TestCookbooks_GetVersionsMissingFromResponse(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/nginx", cinctest.Route{Body: `{"apache2":{"url":"u","versions":[]}}`})
	c := newTestClient(t, srv.Server)

	if entry, _, err := c.Cookbooks.GetVersions(context.Background(), "nginx", ""); err == nil || entry != nil {
		t.Fatalf("GetVersions = %+v, %v; want nil and an error", entry, err)
	}
}

func TestCookbooks_DownloadManifestError(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbooks/nginx/_latest", cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	c := newTestClient(t, srv.Server)

	err := c.Cookbooks.Download(context.Background(), "nginx", LatestVersion, t.TempDir())
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "get cookbook manifest") {
		t.Fatalf("Download err = %v, want a wrapped ErrNotFound", err)
	}
}
