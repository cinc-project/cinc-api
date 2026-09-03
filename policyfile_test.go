package cinc

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestParsePolicyfileLock(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		lock, err := ParsePolicyfileLock([]byte(`{
			"name":"appserver","revision_id":"rev1","run_list":["recipe[base]"],
			"cookbook_locks":{"base":{"version":"1.0.0","identifier":"abc"}}
		}`))
		if err != nil {
			t.Fatalf("ParsePolicyfileLock: %v", err)
		}
		if lock.Name != "appserver" || lock.RevisionID != "rev1" {
			t.Errorf("lock = %+v", lock)
		}
		if cl, ok := lock.CookbookLocks["base"]; !ok || cl.Identifier != "abc" {
			t.Errorf("cookbook locks = %+v", lock.CookbookLocks)
		}
	})
	t.Run("missing name is rejected", func(t *testing.T) {
		if _, err := ParsePolicyfileLock([]byte(`{"revision_id":"r"}`)); err == nil {
			t.Error("expected an error for a lock with no policy name")
		}
	})
	t.Run("malformed JSON is rejected", func(t *testing.T) {
		if _, err := ParsePolicyfileLock([]byte(`{not json`)); err == nil {
			t.Error("expected an error for malformed JSON")
		}
	})
}

func TestLoadPolicyfileLock(t *testing.T) {
	t.Run("reads and parses", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Policyfile.lock.json")
		raw := []byte(`{"name":"web","revision_id":"r1","cookbook_locks":{}}`)
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		lock, data, err := LoadPolicyfileLock(path)
		if err != nil {
			t.Fatalf("LoadPolicyfileLock: %v", err)
		}
		if lock.Name != "web" {
			t.Errorf("name = %q", lock.Name)
		}
		if string(data) != string(raw) {
			t.Errorf("returned bytes were not the original file contents")
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, _, err := LoadPolicyfileLock(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Error("expected an error for a missing lock file")
		}
	})
	t.Run("present but malformed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Policyfile.lock.json")
		if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadPolicyfileLock(path); err == nil {
			t.Error("expected a parse error for a malformed lock file")
		}
	})
}

func TestPushRevision_NoCookbooks(t *testing.T) {
	// A lock with no cookbook locks pushes with a single PutPolicy and no
	// uploads. The body must be sent verbatim — including fields the typed
	// model does not know about ("x_unknown") — so nothing is dropped.
	lockJSON := []byte(`{"name":"appserver","revision_id":"rev1","run_list":["recipe[base]"],"cookbook_locks":{},"x_unknown":"keep-me"}`)

	var gotBody []byte
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/policy_groups/prod/policies/appserver", cinctest.Route{
		Body: `{"revision_id":"rev1","name":"appserver"}`,
		Assert: func(_ *testing.T, _ *http.Request, body []byte) {
			gotBody = body
		},
	})

	c := newTestClient(t, srv.Server)
	rev, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", nil)
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	if rev.RevisionID != "rev1" {
		t.Errorf("revision = %+v", rev)
	}
	if !contains(string(gotBody), "x_unknown") || !contains(string(gotBody), "keep-me") {
		t.Errorf("PutPolicy body dropped unmodeled lock fields: %s", gotBody)
	}
}

func TestPushRevision_Errors(t *testing.T) {
	c := newTestClient(t, cinctest.New(t).Server)
	ctx := context.Background()

	t.Run("cookbook lock without identifier", func(t *testing.T) {
		lock := []byte(`{"name":"p","cookbook_locks":{"base":{"version":"1.0.0"}}}`)
		if _, _, err := c.Policies.PushRevision(ctx, lock, "prod", nil); err == nil {
			t.Error("expected an error for a cookbook lock with no identifier")
		}
	})
	t.Run("no cookbook supplied for a lock", func(t *testing.T) {
		lock := []byte(`{"name":"p","cookbook_locks":{"base":{"identifier":"abc"}}}`)
		if _, _, err := c.Policies.PushRevision(ctx, lock, "prod", map[string]*LocalCookbook{}); err == nil {
			t.Error("expected an error when no cookbook is supplied for a lock")
		}
	})
	t.Run("invalid lock", func(t *testing.T) {
		if _, _, err := c.Policies.PushRevision(ctx, []byte(`{bad`), "prod", nil); err == nil {
			t.Error("expected an error for an invalid lock")
		}
	})
}

func TestPushRevision_UploadFailureIsWrapped(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nginx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nginx", "metadata.rb"), []byte("name 'nginx'\nversion '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb, err := LocalCookbookFromDir(filepath.Join(dir, "nginx"), "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}

	const identifier = "deadbeef567890abcdef1234567890abcdef1234"
	lockJSON := []byte(`{"name":"web","cookbook_locks":{"nginx":{"identifier":"` + identifier + `"}}}`)

	var associated bool
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			w.WriteHeader(500) // the upload fails here
			w.Write([]byte(`{"error":["boom"]}`))
		case r.URL.Path == "/organizations/o/policy_groups/prod/policies/web":
			associated = true
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	c := newTestClient(t, srv.Server)
	_, _, err = c.Policies.PushRevision(context.Background(), lockJSON, "prod", map[string]*LocalCookbook{"nginx": cb})
	if err == nil {
		t.Fatal("expected an error when the artifact upload fails")
	}
	if !contains(err.Error(), "nginx") {
		t.Errorf("error %q should name the cookbook that failed", err.Error())
	}
	if associated {
		t.Error("the revision was associated despite the upload failing")
	}
}

func TestPushRevision_UploadsArtifactsThenAssociates(t *testing.T) {
	// Build a real cookbook on disk so Upload's sandbox flow runs end to end.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nginx", "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	recipe := []byte("package 'nginx'\n")
	if err := os.WriteFile(filepath.Join(dir, "nginx", "metadata.rb"), []byte("name 'nginx'\nversion '1.2.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nginx", "recipes", "default.rb"), recipe, 0o644); err != nil {
		t.Fatal(err)
	}
	cb, err := LocalCookbookFromDir(filepath.Join(dir, "nginx"), "1.2.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}

	const identifier = "abc1234567890abcdef1234567890abcdef12345"
	recipeChecksum := md5Hex(recipe)
	lockJSON := []byte(`{"name":"web","revision_id":"rev9","cookbook_locks":{"nginx":{"version":"1.2.0","identifier":"` + identifier + `"}}}`)

	var artifactUploaded, associated bool
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			uploadURL := "http://" + r.Host + "/upload/" + recipeChecksum
			w.WriteHeader(201)
			w.Write([]byte(`{"sandbox_id":"sb1","checksums":{"` + recipeChecksum + `":{"needs_upload":true,"url":"` + uploadURL + `"}}}`))
		case r.Method == "PUT" && r.URL.Path == "/upload/"+recipeChecksum:
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(200)
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb1":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/cookbook_artifacts/nginx/"+identifier:
			artifactUploaded = true
			w.WriteHeader(200)
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/policy_groups/prod/policies/web":
			associated = true
			body, _ := io.ReadAll(r.Body)
			if !contains(string(body), identifier) {
				t.Errorf("associate body missing the lock: %s", body)
			}
			w.Write([]byte(`{"revision_id":"rev9","name":"web"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	c := newTestClient(t, srv.Server)
	rev, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", map[string]*LocalCookbook{"nginx": cb})
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	if !artifactUploaded {
		t.Error("cookbook artifact was not uploaded")
	}
	if !associated {
		t.Error("policy revision was not associated with the group")
	}
	if rev.RevisionID != "rev9" {
		t.Errorf("revision = %+v", rev)
	}
}
