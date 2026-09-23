package suite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// testCookbookUploadDownload drives the full three-step cookbook upload
// (sandbox -> file PUTs -> manifest PUT) and the download flow, then verifies
// every file round-trips byte-for-byte. This also exercises the parallel
// bookshelf upload/download path end to end.
func testCookbookUploadDownload(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "cookbook")
	cleanup(t, "cookbook "+name, func(ctx context.Context) error {
		_, err := c.Cookbooks.Delete(ctx, name, "1.0.0")
		return err
	})

	// The cookbook name comes from metadata.rb, not the directory. Every file
	// names the cookbook: see uniqueContent.
	src := filepath.Join(t.TempDir(), "src")
	files := map[string]string{
		"metadata.rb": "name '" + name + "'\nversion '1.0.0'\ndescription 'Installs nginx'\n" +
			"depends 'apt'\ndepends 'logrotate', '~> 2.0'\n",
		"recipes/default.rb":       uniqueContent(name, "package 'nginx'\n"),
		"attributes/default.rb":    uniqueContent(name, "default['nginx']['port'] = 80\n"),
		"templates/nginx.conf.erb": uniqueContent(name, "listen <%= node['nginx']['port'] %>;\n"),
	}
	for rel, content := range files {
		writeFile(t, filepath.Join(src, filepath.FromSlash(rel)), content)
	}

	// An empty version takes the one declared in metadata.rb.
	cb, err := cinc.LocalCookbookFromDir(src, "")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	if err := c.Cookbooks.Upload(ctx, cb); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	list, _, err := c.Cookbooks.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list[name]; !ok {
		t.Fatalf("%s not in cookbook list", name)
	}
	got, _, err := c.Cookbooks.Get(ctx, name, "1.0.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CookbookName != name {
		t.Fatalf("cookbook_name = %q, want %q", got.CookbookName, name)
	}
	if n := len(got.AllFiles()); n != len(files) {
		t.Fatalf("manifest lists %d files, want %d", n, len(files))
	}
	// The metadata block — dependencies included — survives the round trip.
	md := got.Metadata
	if md.Name != name || md.Version != "1.0.0" || md.Description != "Installs nginx" {
		t.Errorf("metadata = %+v", md)
	}
	if md.Dependencies["apt"] != ">= 0.0.0" || md.Dependencies["logrotate"] != "~> 2.0" {
		t.Errorf("metadata dependencies = %v", md.Dependencies)
	}
	// Each file is classified into the segment chef-client looks in, so it
	// can find the recipe. How that shows depends on the server API version:
	// the per-segment layout (erchef at API v0/v1) lists the file in the
	// segment's slice under its bare name, while all_files (API v2, and
	// cinc-server-ng at any version) prefixes the name with the segment.
	segments := fileSegments(got)
	for path, segment := range map[string]string{
		"recipes/default.rb":       "recipes",
		"metadata.rb":              "root_files",
		"templates/nginx.conf.erb": "templates",
		"attributes/default.rb":    "attributes",
	} {
		if segments[path] != segment {
			t.Errorf("%s is in segment %q, want %q (all: %v)", path, segments[path], segment, segments)
		}
	}

	dest := t.TempDir()
	if err := c.Cookbooks.Download(ctx, name, "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	for rel, want := range files {
		assertFile(t, filepath.Join(dest, filepath.FromSlash(rel)), want)
	}
}

// testCookbookArtifactUpload uploads a content-addressed cookbook artifact
// (Policyfile mode) and reads it back by identifier.
func testCookbookArtifactUpload(t *testing.T, _ Target, c *cinc.Client) {
	ctx := t.Context()
	name := uniqueName(t, "artifact")
	identifier := randomHex(t, 20)
	cleanup(t, "cookbook artifact "+name, func(ctx context.Context) error {
		_, err := c.CookbookArtifacts.Delete(ctx, name, identifier)
		return err
	})

	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "metadata.rb"), "name '"+name+"'\n")
	writeFile(t, filepath.Join(src, "recipes", "default.rb"), uniqueContent(name, "package 'nginx'\n"))

	cb, err := cinc.LocalCookbookFromDir(src, "0.0.0")
	if err != nil {
		t.Fatalf("LocalCookbookFromDir: %v", err)
	}
	if err := c.CookbookArtifacts.Upload(ctx, cb, identifier); err != nil {
		t.Fatalf("CookbookArtifacts.Upload: %v", err)
	}

	got, _, err := c.CookbookArtifacts.Get(ctx, name, identifier)
	if err != nil {
		t.Fatalf("CookbookArtifacts.Get: %v", err)
	}
	if got.CookbookName != name {
		t.Fatalf("cookbook_name = %q, want %q", got.CookbookName, name)
	}
	// chef-client builds the cookbook from the artifact's metadata block.
	if got.Metadata.Name != name || got.Metadata.Version != "0.0.0" {
		t.Errorf("artifact metadata = %+v", got.Metadata)
	}
}

// fileSegments maps each file's path to the segment the server put it in,
// reading either manifest layout.
func fileSegments(cb *cinc.Cookbook) map[string]string {
	out := map[string]string{}
	for segment, files := range map[string][]cinc.CookbookFileRef{
		"attributes": cb.Attributes, "definitions": cb.Definitions, "files": cb.Files,
		"libraries": cb.Libraries, "providers": cb.Providers, "recipes": cb.Recipes,
		"resources": cb.Resources, "root_files": cb.RootFiles, "templates": cb.Templates,
	} {
		for _, f := range files {
			out[f.Path] = segment
		}
	}
	for _, f := range cb.AllFilesManifest {
		segment, _, nested := strings.Cut(f.Name, "/")
		if !nested {
			segment = "root_files"
		}
		out[f.Path] = segment
	}
	return out
}
