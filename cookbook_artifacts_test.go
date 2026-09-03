package cinc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestCookbookArtifacts_GetVersions(t *testing.T) {
	t.Run("unwraps single-key envelope", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/cookbook_artifacts/rabbitmq", cinctest.Route{
			Body: `{"rabbitmq":{"url":"http://x/rabbitmq","versions":[` +
				`{"url":"http://x/rabbitmq/0bd7","identifier":"0bd7"},` +
				`{"url":"http://x/rabbitmq/0e10","identifier":"0e10"}]}}`,
		})
		c := newTestClient(t, srv.Server)

		entry, resp, err := c.CookbookArtifacts.GetVersions(context.Background(), "rabbitmq")
		if err != nil {
			t.Fatalf("GetVersions: %v", err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if entry == nil || len(entry.Versions) != 2 || entry.Versions[0].Identifier != "0bd7" {
			t.Fatalf("entry = %+v, want 2 identifiers", entry)
		}
	})

	t.Run("propagates 404", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/cookbook_artifacts/missing", cinctest.Route{
			Status: 404, Body: `{"error":["not found"]}`,
		})
		c := newTestClient(t, srv.Server)

		entry, _, err := c.CookbookArtifacts.GetVersions(context.Background(), "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if entry != nil {
			t.Errorf("entry = %+v, want nil on error", entry)
		}
	})
}

func TestCookbookArtifacts_Upload_ErrorWrapped(t *testing.T) {
	// Build a minimal cookbook so LocalCookbookFromDir succeeds, then have the
	// fake server fail the sandbox POST so Upload wraps the error.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "nginx"), 0o755)
	os.WriteFile(filepath.Join(dir, "nginx", "metadata.rb"),
		[]byte("name 'nginx'\nversion '1.2.0'\n"), 0o644)
	cb, err := LocalCookbookFromDir(filepath.Join(dir, "nginx"), "1.2.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}

	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":["sandbox unavailable"]}`))
	})
	c := newTestClient(t, srv.Server)
	uploadErr := c.CookbookArtifacts.Upload(context.Background(), cb, "deadbeef")
	if uploadErr == nil {
		t.Fatal("expected error from failing sandbox POST")
	}
	if !contains(uploadErr.Error(), "upload cookbook artifact") {
		t.Errorf("error %q should be wrapped with artifact context", uploadErr.Error())
	}
}

func TestCookbookArtifacts_Delete(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("DELETE /organizations/o/cookbook_artifacts/nginx/abc123",
		cinctest.Route{Body: `{}`})
	c := newTestClient(t, srv.Server)

	resp, err := c.CookbookArtifacts.Delete(context.Background(), "nginx", "abc123")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if resp == nil || resp.StatusCode != 200 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestCookbookArtifacts_GetAndList(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/cookbook_artifacts",
		cinctest.Route{Body: `{"nginx":{"url":"http://x","versions":[{"identifier":"abc123","url":"http://x/abc123"}]}}`})
	srv.Handle("GET /organizations/o/cookbook_artifacts/nginx/abc123",
		cinctest.Route{Body: `{"cookbook_name":"nginx","version":"abc123","name":"nginx"}`})
	c := newTestClient(t, srv.Server)
	ctx := context.Background()

	list, _, err := c.CookbookArtifacts.List(ctx)
	if err != nil || len(list["nginx"].Versions) != 1 {
		t.Fatalf("List: %+v %v", list, err)
	}
	cb, _, err := c.CookbookArtifacts.Get(ctx, "nginx", "abc123")
	if err != nil || cb.CookbookName != "nginx" {
		t.Fatalf("Get: %+v %v", cb, err)
	}
}

func TestCookbookArtifacts_UploadRoundTrip(t *testing.T) {
	// Build a cookbook on disk: metadata.rb + recipes/default.rb.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "nginx", "recipes"), 0o755)
	metadataContent := []byte("name 'nginx'\nversion '1.2.0'\n")
	recipeContent := []byte("package 'nginx'\n")
	os.WriteFile(filepath.Join(dir, "nginx", "metadata.rb"), metadataContent, 0o644)
	os.WriteFile(filepath.Join(dir, "nginx", "recipes", "default.rb"), recipeContent, 0o644)

	cb, err := LocalCookbookFromDir(filepath.Join(dir, "nginx"), "1.2.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}

	const identifier = "abc1234567890abcdef1234567890abcdef12345"

	// Pick recipes/default.rb as the one needing upload.
	recipeChecksum := md5Hex(recipeContent)

	var manifestBody []byte
	uploaded := map[string][]byte{}
	srv := cinctest.New(t)
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/organizations/o/sandboxes":
			uploadURL := "http://" + r.Host + "/upload/" + recipeChecksum
			w.WriteHeader(201)
			w.Write([]byte(`{"sandbox_id":"sb2","checksums":{"` + recipeChecksum + `":{"needs_upload":true,"url":"` + uploadURL + `"}}}`))
		case r.Method == "PUT" && r.URL.Path == "/upload/"+recipeChecksum:
			// Pre-signed bookshelf upload — must NOT carry Chef signing header.
			if r.Header.Get("X-Ops-Authorization-1") != "" {
				t.Errorf("file upload PUT carried Chef signing header (should be unsigned)")
			}
			body, _ := io.ReadAll(r.Body)
			uploaded[recipeChecksum] = body
			w.WriteHeader(200)
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/sandboxes/sb2":
			w.Write([]byte(`{}`))
		case r.Method == "PUT" && r.URL.Path == "/organizations/o/cookbook_artifacts/nginx/"+identifier:
			manifestBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	c := newTestClient(t, srv.Server)
	if err := c.CookbookArtifacts.Upload(context.Background(), cb, identifier); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Issue 3: verify the file-upload PUT was exercised.
	got, ok := uploaded[recipeChecksum]
	if !ok {
		t.Fatal("expected recipes/default.rb to be uploaded, but no PUT was received")
	}
	if string(got) != string(recipeContent) {
		t.Errorf("uploaded recipes/default.rb = %q, want %q", got, recipeContent)
	}

	// Verify manifest was sent and contains identifier + correct chef_type.
	if len(manifestBody) == 0 {
		t.Fatal("no manifest body received")
	}
	body := string(manifestBody)
	if !contains(body, identifier) {
		t.Errorf("manifest missing identifier: %s", body)
	}
	if !contains(body, "cookbook_artifact_version") {
		t.Errorf("manifest missing chef_type cookbook_artifact_version: %s", body)
	}
}
