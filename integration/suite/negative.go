package suite

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// The negative cases send requests this client never sends, to check that the
// server rejects them the way erchef does. Each one is the shape of a bug the
// client once had, so a server that accepts them cannot catch that bug.

// testRejectsManifestWithoutMetadata: erchef's cookbook spec requires a
// metadata block (chef_cookbook_version:cookbook_spec_v2).
func testRejectsManifestWithoutMetadata(t *testing.T, tgt Target, c *cinc.Client) {
	name, files := cookbookFilesForManifest(t, c)
	status, body := putManifest(t, tgt, c, name, "2", map[string]any{
		"name": name + "-2.0.0", "cookbook_name": name, "version": "2.0.0",
		"json_class": "Chef::CookbookVersion", "chef_type": "cookbook_version",
		"all_files": files,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("PUT manifest without metadata: status %d (%s), want 400", status, body)
	}
}

// testRejectsAllFilesUnderAPIv1: at server API versions 0 and 1 erchef
// validates the per-segment manifest, where all_files is an invalid key
// (chef_cookbook_version:wants_all_files).
func testRejectsAllFilesUnderAPIv1(t *testing.T, tgt Target, c *cinc.Client) {
	name, files := cookbookFilesForManifest(t, c)
	status, body := putManifest(t, tgt, c, name, "1", map[string]any{
		"name": name + "-2.0.0", "cookbook_name": name, "version": "2.0.0",
		"json_class": "Chef::CookbookVersion", "chef_type": "cookbook_version",
		"metadata":  map[string]any{"name": name, "version": "2.0.0"},
		"all_files": files,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("PUT all_files manifest under API v1: status %d (%s), want 400", status, body)
	}
}

// testRejectsKeyFieldOnClientUpdate: under API v1 keys are managed through
// the keys endpoints, and a client PUT carrying create_key is rejected
// (chef_key_base: key_management_not_supported).
func testRejectsKeyFieldOnClientUpdate(t *testing.T, tgt Target, c *cinc.Client) {
	client := newClient(t, c)
	body, err := json.Marshal(map[string]any{"name": client, "create_key": true})
	if err != nil {
		t.Fatal(err)
	}
	status, resp := rawRequest(t, tgt, http.MethodPut, "/organizations/"+tgt.Org+"/clients/"+client, "1", body)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT client with create_key: status %d (%s), want 400", status, resp)
	}
}

// cookbookFilesForManifest uploads version 1.0.0 of a new cookbook, so its
// file contents are on the server, and returns the name and that version's
// files as all_files entries. A manifest for another version built from them
// can then only be rejected for its shape, never for a missing checksum.
func cookbookFilesForManifest(t *testing.T, c *cinc.Client) (string, []map[string]string) {
	t.Helper()
	name := uniqueName(t, "cookbook")
	uploadCookbook(t, c, name, "1.0.0", nil)
	cb, _, err := c.Cookbooks.Get(t.Context(), name, "1.0.0")
	if err != nil {
		t.Fatalf("get uploaded cookbook: %v", err)
	}
	var files []map[string]string
	for _, f := range cb.AllFiles() {
		files = append(files, map[string]string{
			"name": f.Path, "path": f.Path, "checksum": f.Checksum, "specificity": f.Specificity,
		})
	}
	if len(files) == 0 {
		t.Fatal("uploaded cookbook has no files")
	}
	return name, files
}

// putManifest PUTs manifest as version 2.0.0 of name at the given server API
// version. A server that wrongly accepts it gets the version deleted again.
func putManifest(t *testing.T, tgt Target, c *cinc.Client, name, apiVersion string, manifest map[string]any) (int, []byte) {
	t.Helper()
	cleanup(t, "cookbook "+name+" 2.0.0", func(ctx context.Context) error {
		_, err := c.Cookbooks.Delete(ctx, name, "2.0.0")
		return err
	})
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return rawRequest(t, tgt, http.MethodPut, "/organizations/"+tgt.Org+"/cookbooks/"+name+"/2.0.0", apiVersion, body)
}
