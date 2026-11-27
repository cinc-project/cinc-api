package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// manifestPUT runs an upload of a small cookbook against a fake server and
// returns the manifest PUT's query string and decoded body. The manifest PUT
// answers with status and body.
func manifestPUT(t *testing.T, status int, body string, opts ...CookbookUploadOption) (query string, manifest map[string]any, err error) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "web")
	writeTree(t, root, manifestFixture)
	cb, lerr := LocalCookbookFromDir(root, "1.2.0")
	if lerr != nil {
		t.Fatal(lerr)
	}
	key := testRSAKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			w.WriteHeader(201)
			w.Write([]byte(`{"sandbox_id":"sb","checksums":{}}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/cookbooks/web/1.2.0":
			verifySignature(t, r, key)
			query = r.URL.RawQuery
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &manifest); err != nil {
				t.Errorf("manifest: %v", err)
			}
			w.WriteHeader(status)
			w.Write([]byte(body))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	recordSleeps(c)
	err = c.Cookbooks.Upload(context.Background(), cb, opts...)
	return query, manifest, err
}

// By default the manifest says nothing about freezing: erchef then keeps a
// frozen version frozen and leaves any other unfrozen (chef_wm_cookbook_version
// from_json).
func TestCookbooks_Upload_DefaultNeitherFreezesNorForces(t *testing.T) {
	query, manifest, err := manifestPUT(t, 200, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest["frozen?"]; ok || query != "" {
		t.Errorf("frozen? = %v, query = %q; want neither", manifest["frozen?"], query)
	}
}

// WithFreeze uploads the version frozen, as knife cookbook upload --freeze.
func TestCookbooks_Upload_WithFreeze(t *testing.T) {
	_, manifest, err := manifestPUT(t, 200, `{}`, WithFreeze())
	if err != nil {
		t.Fatal(err)
	}
	if manifest["frozen?"] != true {
		t.Errorf("frozen? = %v, want true", manifest["frozen?"])
	}
}

// WithForce overwrites a frozen version, as knife cookbook upload --force:
// erchef reads ?force= (any value but "false"). The signature still covers the
// path without the query, which verifySignature checks.
func TestCookbooks_Upload_WithForce(t *testing.T) {
	query, _, err := manifestPUT(t, 200, `{}`, WithForce())
	if err != nil {
		t.Fatal(err)
	}
	if query != "force=true" {
		t.Errorf("query = %q, want force=true", query)
	}
}

// A frozen version answers 409 with chef_wm_cookbook_version's
// conflict_message; that is ErrCookbookFrozen, and still ErrConflict.
func TestCookbooks_Upload_FrozenVersion(t *testing.T) {
	_, _, err := manifestPUT(t, http.StatusConflict,
		`{"error":["The cookbook web at version 1.2.0 is frozen. Use the 'force' option to override."]}`)
	if !errors.Is(err, ErrCookbookFrozen) || !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrCookbookFrozen and ErrConflict", err)
	}
	if _, ok := errors.AsType[*ErrorResponse](err); !ok {
		t.Errorf("err = %v, want it to carry the *ErrorResponse", err)
	}

	// Any other conflict is not a frozen cookbook.
	_, _, err = manifestPUT(t, http.StatusConflict, `{"error":["Cookbook already exists"]}`)
	if errors.Is(err, ErrCookbookFrozen) || !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict only", err)
	}
}

// Get reports whether a version is frozen.
func TestCookbook_Frozen(t *testing.T) {
	var cb Cookbook
	if err := json.Unmarshal([]byte(`{"cookbook_name":"web","name":"web-1.2.0","version":"1.2.0","frozen?":true}`), &cb); err != nil {
		t.Fatal(err)
	}
	if !cb.Frozen {
		t.Error("Frozen = false, want true")
	}
}
