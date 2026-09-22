package cinc

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The expectations below follow Chef::Cookbook::Metadata (chef
// lib/chef/cookbook/metadata.rb): `depends`/`supports` store
// VersionConstraint#to_s ("OP VERSION", ">= 0.0.0" when omitted, "= X" for a
// bare version), `version` stores Chef::Version#to_s (x.y becomes x.y.0), and
// chef_version/ohai_version serialize as sorted "OP VERSION" lists
// (gem_requirements_to_array).
func TestParseMetadataRb(t *testing.T) {
	cases := []struct {
		desc string
		src  string
		want CookbookMetadata
	}{
		{"strings, both quote styles, parens", `
name 'nginx'
version "1.2.3"
description('Installs nginx')
long_description "Longer."
maintainer 'Sous Chefs'   # trailing comment
maintainer_email 'help@example.com'
license 'Apache-2.0'
source_url 'https://example.com/nginx'
issues_url 'https://example.com/nginx/issues'
`, CookbookMetadata{
			Name: "nginx", Version: "1.2.3", Description: "Installs nginx",
			LongDescription: "Longer.", Maintainer: "Sous Chefs",
			MaintainerEmail: "help@example.com", License: "Apache-2.0",
			SourceURL: "https://example.com/nginx", IssuesURL: "https://example.com/nginx/issues",
		}},
		{"version x.y is padded like Chef::Version", "version '2.0'\n",
			CookbookMetadata{Version: "2.0.0"}},
		{"depends and supports constraints", `
depends 'apt'
depends 'yum', '>= 3.0'
depends "compat_resource", "~> 12.19"
depends('logrotate', '1.2')
depends 'build-essential', '>=8.0'
supports 'ubuntu'
supports 'redhat', '>= 7.0'
`, CookbookMetadata{
			Dependencies: map[string]string{
				"apt": ">= 0.0.0", "yum": ">= 3.0", "compat_resource": "~> 12.19",
				"logrotate": "= 1.2", "build-essential": ">= 8.0",
			},
			Platforms: map[string]string{"ubuntu": ">= 0.0.0", "redhat": ">= 7.0"},
		}},
		{"chef_version and ohai_version", `
chef_version '>= 15.3'
chef_version '< 19', '>= 16'
ohai_version '16.0'
`, CookbookMetadata{
			ChefVersions: [][]string{{">= 15.3"}, {"< 19", ">= 16"}},
			OhaiVersions: [][]string{{"= 16.0"}},
		}},
		{"privacy", "privacy true\n", CookbookMetadata{Privacy: true}},
		{"escaped quotes", `description 'it\'s a \\ path'` + "\n" + `maintainer "say \"hi\""` + "\n" +
			`license 'C:\dir'` + "\n",
			CookbookMetadata{Description: `it's a \ path`, Maintainer: `say "hi"`, License: `C:\dir`}},
		{"later calls win", "version '1.0.0'\nversion '1.1.0'\n", CookbookMetadata{Version: "1.1.0"}},
		{"non-literal calls are not recognized", `
name File.basename(__dir__)
# version '9.9.9'
version "#{major}.0.0"
description "tab\tin a double-quoted string"
long_description IO.read(File.join(File.dirname(__FILE__), 'README.md'))
maintainer 'a' + 'b'
license 'MIT' if true
depends 'x',
  '>= 1.0'
depends pkg
supports %w(ubuntu debian)
chef_version
privacy 'yes'
recipe 'nginx::default', 'Installs nginx'
name 'unterminated
name 'x' 'y'
name('x'
name 'a', 'b'
version '1.0', '2.0'
privacy trueish
depends 'apt', true
`, CookbookMetadata{}},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got, err := parseMetadataRb([]byte(c.src))
			if err != nil {
				t.Fatalf("parseMetadataRb: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// Chef raises while loading metadata.rb for these, so the cookbook could not
// be uploaded by knife either; failing loudly beats sending a manifest the
// server rejects or silently dropping a dependency.
func TestParseMetadataRb_Errors(t *testing.T) {
	cases := map[string]string{
		"bad version":            "version '1.2.3.4'\n",
		"version out of range":   "version '99999999999999999999.0'\n",
		"bad constraint version": "depends 'apt', '>= 1.2.3.4'\n",
		"bad lone version":       "depends 'apt', '1.2.3.4'\n",
		"bad depends constraint": "depends 'apt', '>> 1.0'\n",
		"multiple constraints":   "depends 'apt', '>= 1.0', '< 2.0'\n",
		"self dependency":        "name 'apt'\ndepends 'apt'\n",
		"bad supports":           "supports 'ubuntu', 'latest'\n",
		"bad chef_version":       "chef_version 'banana'\n",
	}
	for desc, src := range cases {
		t.Run(desc, func(t *testing.T) {
			_, err := parseMetadataRb([]byte(src))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "metadata.rb:") {
				t.Errorf("error %q should name the metadata.rb line", err)
			}
		})
	}
}

func TestParseMetadataJSON(t *testing.T) {
	src := `{
		"name": "apache2", "version": "8.6.0", "description": "d",
		"long_description": "ld", "maintainer": "m", "maintainer_email": "e",
		"license": "Apache-2.0", "source_url": "s", "issues_url": "i",
		"privacy": true, "eager_load_libraries": false,
		"dependencies": {"logrotate": ">= 0.0.0", "iptables": [">= 1.0"]},
		"platforms": {"ubuntu": ">= 0.0.0"},
		"providing": {"apache2": ">= 0.0.0"},
		"recipes": {"apache2::default": "Installs apache2"},
		"attributes": {"apache/port": {"default": "80"}},
		"chef_versions": [[">= 15.3"]], "ohai_versions": [], "gems": [["rack", ">= 2"]]
	}`
	got, err := parseMetadataJSON([]byte(src))
	if err != nil {
		t.Fatalf("parseMetadataJSON: %v", err)
	}
	want := CookbookMetadata{
		Name: "apache2", Version: "8.6.0", Description: "d", LongDescription: "ld",
		Maintainer: "m", MaintainerEmail: "e", License: "Apache-2.0",
		SourceURL: "s", IssuesURL: "i", Privacy: true, EagerLoadLibraries: false,
		// Chef's handle_incorrect_constraints unwraps a one-element array.
		Dependencies: map[string]string{"logrotate": ">= 0.0.0", "iptables": ">= 1.0"},
		Platforms:    map[string]string{"ubuntu": ">= 0.0.0"},
		Providing:    map[string]string{"apache2": ">= 0.0.0"},
		Recipes:      map[string]string{"apache2::default": "Installs apache2"},
		Attributes:   map[string]any{"apache/port": map[string]any{"default": "80"}},
		ChefVersions: [][]string{{">= 15.3"}}, OhaiVersions: [][]string{},
		Gems: [][]string{{"rack", ">= 2"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseMetadataJSON_Errors(t *testing.T) {
	cases := map[string]string{
		"not json":                    `{`,
		"multi-constraint dependency": `{"dependencies": {"apt": [">= 1.0", "< 2.0"]}}`,
		"empty-array dependency":      `{"dependencies": {"apt": []}}`,
		"non-string dependency":       `{"dependencies": {"apt": 1}}`,
		"non-string array dependency": `{"dependencies": {"apt": [1]}}`,
	}
	for desc, src := range cases {
		t.Run(desc, func(t *testing.T) {
			if _, err := parseMetadataJSON([]byte(src)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadCookbookMetadata(t *testing.T) {
	t.Run("metadata.json wins over metadata.rb", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{
			"metadata.json": `{"name":"fromjson","version":"1.0.0"}`,
			"metadata.rb":   "name 'fromrb'\nversion '2.0.0'\n",
		})
		md, err := loadCookbookMetadata(dir)
		if err != nil {
			t.Fatal(err)
		}
		if md.Name != "fromjson" || md.Version != "1.0.0" {
			t.Fatalf("got %+v", md)
		}
	})
	t.Run("no metadata is the zero value", func(t *testing.T) {
		md, err := loadCookbookMetadata(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(md, CookbookMetadata{}) {
			t.Fatalf("got %+v", md)
		}
	})
	t.Run("metadata.rb errors are reported", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"metadata.rb": "version 'x'\n"})
		if _, err := loadCookbookMetadata(dir); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("metadata.json that is a directory is an error", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "metadata.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCookbookMetadata(dir); err == nil {
			t.Fatal("expected an error")
		}
	})
}
