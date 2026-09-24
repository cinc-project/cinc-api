package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	res, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", nil)
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	if res.Revision.RevisionID != "rev1" {
		t.Errorf("revision = %+v", res.Revision)
	}
	if len(res.Uploaded) != 0 || len(res.AlreadyPresent) != 0 {
		t.Errorf("result = %+v, want no artifacts", res)
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
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbook_artifacts":
			w.Write([]byte(`{}`))
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
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbook_artifacts":
			w.Write([]byte(`{}`))
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
	res, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", map[string]*LocalCookbook{"nginx": cb})
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	rev := res.Revision
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

// pushTestCookbook writes a minimal cookbook named name to disk and loads it.
func pushTestCookbook(t *testing.T, name string) *LocalCookbook {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.rb"), []byte("name '"+name+"'\nversion '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb, err := LocalCookbookFromDir(dir, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	return cb
}

func TestPushRevision_SkipsArtifactsTheServerHas(t *testing.T) {
	// Like chef-cli's uploader, PushRevision lists the server's artifacts once
	// and uploads only identifiers it lacks: a real Chef Server answers a PUT
	// of an existing artifact identifier with 409. "nginx" is present under the
	// locked identifier and must be skipped; "base" is present only under a
	// different identifier and must still be uploaded.
	const nginxID = "1111111111111111111111111111111111111111"
	const baseID = "2222222222222222222222222222222222222222"
	lockJSON := []byte(`{"name":"web","cookbook_locks":{` +
		`"nginx":{"identifier":"` + nginxID + `"},` +
		`"base":{"identifier":"` + baseID + `"}}}`)

	var lists int
	var uploaded []string
	var associated bool
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbook_artifacts":
			lists++
			// Shape from erchef oc_chef_wm_named_cookbook_artifact:to_json/2.
			w.Write([]byte(`{
				"nginx":{"url":"http://x/cookbook_artifacts/nginx","versions":[
					{"url":"http://x/cookbook_artifacts/nginx/` + nginxID + `","identifier":"` + nginxID + `"}]},
				"base":{"url":"http://x/cookbook_artifacts/base","versions":[
					{"url":"http://x/cookbook_artifacts/base/old","identifier":"old"}]}
			}`))
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			w.WriteHeader(201)
			w.Write([]byte(`{"sandbox_id":"sb1","checksums":{}}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb1":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && contains(r.URL.Path, "/cookbook_artifacts/"):
			uploaded = append(uploaded, r.URL.Path)
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/policy_groups/prod/policies/web":
			associated = true
			w.Write([]byte(`{"name":"web"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	c := newTestClient(t, srv.Server)
	cookbooks := map[string]*LocalCookbook{"nginx": pushTestCookbook(t, "nginx"), "base": pushTestCookbook(t, "base")}
	res, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", cookbooks)
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	if !slices.Equal(res.Uploaded, []string{"base"}) || !slices.Equal(res.AlreadyPresent, []string{"nginx"}) {
		t.Errorf("result = %+v, want base uploaded and nginx already present", res)
	}
	if lists != 1 {
		t.Errorf("listed cookbook artifacts %d times, want once", lists)
	}
	want := "/organizations/o/cookbook_artifacts/base/" + baseID
	if len(uploaded) != 1 || uploaded[0] != want {
		t.Errorf("uploaded %v, want only %s", uploaded, want)
	}
	if !associated {
		t.Error("policy revision was not associated with the group")
	}
}

func TestPushRevision_ConflictMeansAlreadyUploaded(t *testing.T) {
	// A concurrent push can upload the same artifact between our list and our
	// PUT. The identifier is a content hash, so the server's 409 means the
	// artifact is present and the push should carry on.
	const identifier = "3333333333333333333333333333333333333333"
	lockJSON := []byte(`{"name":"web","cookbook_locks":{"nginx":{"identifier":"` + identifier + `"}}}`)

	var associated bool
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbook_artifacts":
			w.Write([]byte(`{}`))
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			w.WriteHeader(201)
			w.Write([]byte(`{"sandbox_id":"sb1","checksums":{}}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb1":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/cookbook_artifacts/nginx/"+identifier:
			w.WriteHeader(409)
			w.Write([]byte(`{"error":"Cookbook artifact already exists"}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/policy_groups/prod/policies/web":
			associated = true
			w.Write([]byte(`{"name":"web"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	c := newTestClient(t, srv.Server)
	cookbooks := map[string]*LocalCookbook{"nginx": pushTestCookbook(t, "nginx")}
	res, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", cookbooks)
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	if !associated {
		t.Error("policy revision was not associated after a 409 on the artifact")
	}
	if len(res.Uploaded) != 0 || !slices.Equal(res.AlreadyPresent, []string{"nginx"}) {
		t.Errorf("result = %+v, want nginx already present", res)
	}
}

func TestPushRevision_ListFailure(t *testing.T) {
	lockJSON := []byte(`{"name":"web","cookbook_locks":{"nginx":{"identifier":"abc"}}}`)
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/organizations/o/cookbook_artifacts" {
			w.WriteHeader(403)
			w.Write([]byte(`{"error":["forbidden"]}`))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})

	c := newTestClient(t, srv.Server)
	cookbooks := map[string]*LocalCookbook{"nginx": pushTestCookbook(t, "nginx")}
	_, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", cookbooks)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestParsePolicyfileLock_ValidatesNames(t *testing.T) {
	// The rules are erchef's chef_regex: policy_file_name and
	// policy_file_revision_id and policy_identifier are NAME_REGEX_MAX_255
	// ([.[:alnum:]_:-]{1,255}); cookbook_locks keys are cookbook_name
	// ([.[:alnum:]_-]+, no colon).
	lock := func(name, revision, cookbook, identifier string) []byte {
		return []byte(`{"name":` + strconvQuote(name) + `,"revision_id":` + strconvQuote(revision) +
			`,"cookbook_locks":{` + strconvQuote(cookbook) + `:{"version":"1.0.0","identifier":` + strconvQuote(identifier) + `}}}`)
	}
	long := strings.Repeat("a", 255)
	valid := []struct{ name, revision, cookbook, identifier string }{
		{"app_server-1.2:blue", "rev1", "base", "abc"},
		{long, long, "my.cook_book-2", long},
	}
	for _, v := range valid {
		if _, err := ParsePolicyfileLock(lock(v.name, v.revision, v.cookbook, v.identifier)); err != nil {
			t.Errorf("ParsePolicyfileLock(%+v): %v", v, err)
		}
	}
	invalid := []struct {
		label, name, revision, cookbook, identifier, want string
	}{
		{"quote in policy name", "web'; evil", "rev1", "base", "abc", "policy name"},
		{"slash in policy name", "a/b", "rev1", "base", "abc", "policy name"},
		{"256-character policy name", long + "a", "rev1", "base", "abc", "policy name"},
		{"space in revision id", "web", "rev 1", "base", "abc", "revision id"},
		{"colon in cookbook lock name", "web", "rev1", "my:base", "abc", "cookbook lock name"},
		{"slash in cookbook lock name", "web", "rev1", "../base", "abc", "cookbook lock name"},
		{"slash in identifier", "web", "rev1", "base", "../abc", "identifier"},
	}
	for _, tc := range invalid {
		t.Run(tc.label, func(t *testing.T) {
			_, err := ParsePolicyfileLock(lock(tc.name, tc.revision, tc.cookbook, tc.identifier))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention the %s", err, tc.want)
			}
		})
	}
	t.Run("empty revision id and identifier are left to the caller", func(t *testing.T) {
		if _, err := ParsePolicyfileLock([]byte(`{"name":"web","cookbook_locks":{"base":{}}}`)); err != nil {
			t.Errorf("ParsePolicyfileLock: %v", err)
		}
	})
}

func strconvQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// pushServer answers the requests of a push that uploads fileless artifacts,
// recording each artifact PUT path.
func pushServer(t *testing.T, uploaded *[]string) *cinctest.Server {
	t.Helper()
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/organizations/o/cookbook_artifacts":
			w.Write([]byte(`{}`))
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			w.WriteHeader(201)
			w.Write([]byte(`{"sandbox_id":"sb1","checksums":{}}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb1":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/organizations/o/cookbook_artifacts/"):
			body, _ := io.ReadAll(r.Body)
			var m struct {
				Name     string `json:"name"`
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal(body, &m); err != nil {
				t.Errorf("artifact manifest: %v", err)
			}
			*uploaded = append(*uploaded, r.URL.Path+" name="+m.Name+" metadata.name="+m.Metadata.Name)
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/organizations/o/policy_groups/prod/policies/"):
			w.Write([]byte(`{"name":"web"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	return srv
}

func TestPushRevision_UploadsUnderTheLockName(t *testing.T) {
	// A cookbook fetched into a cache directory whose metadata does not name
	// it (metadata.rb computes the name, say) takes its name from the
	// directory. PushRevision uploads it under the lock's name instead, and
	// leaves the caller's LocalCookbook as it was.
	dir := filepath.Join(t.TempDir(), "nginx-1.0.0-supermarket")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.rb"), []byte("version '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb, err := LocalCookbookFromDir(dir, "1.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	const identifier = "4444444444444444444444444444444444444444"
	lockJSON := []byte(`{"name":"web","cookbook_locks":{"nginx":{"version":"1.0.0","identifier":"` + identifier + `"}}}`)

	var uploaded []string
	c := newTestClient(t, pushServer(t, &uploaded).Server)
	res, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", map[string]*LocalCookbook{"nginx": cb})
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	want := "/organizations/o/cookbook_artifacts/nginx/" + identifier + " name=nginx metadata.name=nginx"
	if len(uploaded) != 1 || uploaded[0] != want {
		t.Errorf("uploaded %v, want [%s]", uploaded, want)
	}
	if !slices.Equal(res.Uploaded, []string{"nginx"}) {
		t.Errorf("Uploaded = %v, want [nginx]", res.Uploaded)
	}
	if cb.Name != "nginx-1.0.0-supermarket" || cb.Metadata.Name != "nginx-1.0.0-supermarket" {
		t.Errorf("caller's cookbook was renamed to %q / %q", cb.Name, cb.Metadata.Name)
	}
}

func TestPushRevision_RejectsACookbookThatDisagreesWithItsLock(t *testing.T) {
	// Nothing may reach the server: the checks run before the artifact list.
	c := newTestClient(t, cinctest.New(t).Server)
	ctx := context.Background()

	t.Run("metadata names another cookbook", func(t *testing.T) {
		lock := []byte(`{"name":"web","cookbook_locks":{"nginx":{"identifier":"abc"}}}`)
		_, _, err := c.Policies.PushRevision(ctx, lock, "prod", map[string]*LocalCookbook{"nginx": pushTestCookbook(t, "apache2")})
		if err == nil {
			t.Fatal("expected an error when the cookbook's metadata names another cookbook")
		}
		for _, want := range []string{`"nginx"`, `"apache2"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should mention %s", err, want)
			}
		}
	})
	t.Run("version differs from the lock", func(t *testing.T) {
		lock := []byte(`{"name":"web","cookbook_locks":{"nginx":{"version":"2.0.0","identifier":"abc"}}}`)
		_, _, err := c.Policies.PushRevision(ctx, lock, "prod", map[string]*LocalCookbook{"nginx": pushTestCookbook(t, "nginx")})
		if err == nil {
			t.Fatal("expected an error when the cookbook's version differs from the lock's")
		}
		for _, want := range []string{"2.0.0", "1.0.0"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should mention %s", err, want)
			}
		}
	})
}

func TestPushRevision_ResultListsAreSorted(t *testing.T) {
	lockJSON := []byte(`{"name":"web","cookbook_locks":{` +
		`"zlib":{"identifier":"z1"},"apt":{"identifier":"a1"},"mysql":{"identifier":"m1"}}}`)
	var uploaded []string
	c := newTestClient(t, pushServer(t, &uploaded).Server)
	cookbooks := map[string]*LocalCookbook{
		"zlib": pushTestCookbook(t, "zlib"), "apt": pushTestCookbook(t, "apt"), "mysql": pushTestCookbook(t, "mysql"),
	}
	res, _, err := c.Policies.PushRevision(context.Background(), lockJSON, "prod", cookbooks)
	if err != nil {
		t.Fatalf("PushRevision: %v", err)
	}
	if !slices.Equal(res.Uploaded, []string{"apt", "mysql", "zlib"}) {
		t.Errorf("Uploaded = %v, want sorted", res.Uploaded)
	}
	if res.AlreadyPresent == nil || len(res.AlreadyPresent) != 0 {
		t.Errorf("AlreadyPresent = %#v, want an empty, non-nil slice", res.AlreadyPresent)
	}
}

func TestPushRevision_AssociationFailure(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("PUT /organizations/o/policy_groups/prod/policies/web", cinctest.Route{
		Status: 403, Body: `{"error":["forbidden"]}`,
	})
	c := newTestClient(t, srv.Server)
	res, resp, err := c.Policies.PushRevision(context.Background(), []byte(`{"name":"web"}`), "prod", nil)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if res != nil {
		t.Errorf("result = %+v, want nil on failure", res)
	}
	if resp == nil || resp.StatusCode != 403 {
		t.Errorf("response = %+v, want the 403", resp)
	}
}
