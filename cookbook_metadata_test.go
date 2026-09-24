package cinc

import (
	"errors"
	"io/fs"
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
		{"provides, recipe, gem", `
provides 'nginx::default'
provides 'service[nginx]', '>= 1.0'
recipe 'nginx::default', 'Installs nginx'
recipe('nginx::source', "Builds nginx")
gem 'rack'
gem 'aws-sdk', '~> 3.0', '>= 3.1'
`, CookbookMetadata{
			Providing: map[string]string{"nginx::default": ">= 0.0.0", "service[nginx]": ">= 1.0"},
			Recipes:   map[string]string{"nginx::default": "Installs nginx", "nginx::source": "Builds nginx"},
			Gems:      [][]string{{"rack"}, {"aws-sdk", "~> 3.0", ">= 3.1"}},
		}},
		{"eager_load_libraries false", "eager_load_libraries false\n",
			CookbookMetadata{EagerLoadLibraries: false}},
		{"eager_load_libraries glob", "eager_load_libraries 'default.rb'\n",
			CookbookMetadata{EagerLoadLibraries: "default.rb"}},
		{"eager_load_libraries list", `
eager_load_libraries [
  'helpers.rb', # comment
  :"other.rb",
]
privacy false
`, CookbookMetadata{EagerLoadLibraries: []string{"helpers.rb", "other.rb"}}},
		{"eager_load_libraries empty list", "eager_load_libraries([])\n",
			CookbookMetadata{EagerLoadLibraries: []string{}}},
		// validate_version_constraint parses every constraint with
		// Chef::VersionConstraint::Platform, whose versions may be "x" or
		// FreeBSD's "x.y-RELEASE", and to_s keeps the version as written.
		{"platform-style constraint versions", `
depends 'apt', '7'
depends 'yum', '~> 5'
supports 'freebsd', '>= 10.1-RELEASE'
supports 'centos', '> 7.2.1511'
`, CookbookMetadata{
			Dependencies: map[string]string{"apt": "= 7", "yum": "~> 5"},
			Platforms:    map[string]string{"freebsd": ">= 10.1-RELEASE", "centos": "> 7.2.1511"},
		}},
		// Gem::Requirement drops repeated requirement strings.
		{"repeated chef_version requirements", "chef_version '>= 16', '>= 16'\n",
			CookbookMetadata{ChefVersions: [][]string{{">= 16"}}}},
		// Chef compares against the name set so far, so a dependency
		// declared before the name is kept.
		{"depends before name is not a self-dependency", "depends 'apt'\nname 'apt'\n",
			CookbookMetadata{Name: "apt", Dependencies: map[string]string{"apt": ">= 0.0.0"}}},
		{"a literal version after a computed one wins", "version IO.read('VERSION')\nversion '1.0.0'\n",
			CookbookMetadata{Version: "1.0.0"}},
		{"escaped quotes", `description 'it\'s a \\ path'` + "\n" + `maintainer "say \"hi\""` + "\n" +
			`license 'C:\dir'` + "\n",
			CookbookMetadata{Description: `it's a \ path`, Maintainer: `say "hi"`, License: `C:\dir`}},
		{"later calls win", "version '1.0.0'\nversion '1.1.0'\n", CookbookMetadata{Version: "1.1.0"}},
		// Ruby reads a call on, past a trailing comma or an open parenthesis,
		// so Chef sees each of these dependencies; skipping them would upload
		// the cookbook without them.
		{"calls spread over several lines", `
depends 'apt',
        '>= 7.0'
depends(
  'yum',  # a comment
  '~> 5.0'
)
depends('logrotate',
        '1.2')
supports 'ubuntu', # comment after the comma
         '>= 20.04'
chef_version '>= 16',
             '< 19'
depends 'plain'
`, CookbookMetadata{
			Dependencies: map[string]string{
				"apt": ">= 7.0", "yum": "~> 5.0", "logrotate": "= 1.2", "plain": ">= 0.0.0",
			},
			Platforms:    map[string]string{"ubuntu": ">= 20.04"},
			ChefVersions: [][]string{{"< 19", ">= 16"}},
		}},
		{"symbols are read as strings", `
depends :apt
depends :"build-essential", '>= 8.0'
supports :ubuntu
`, CookbookMetadata{
			Dependencies: map[string]string{"apt": ">= 0.0.0", "build-essential": ">= 8.0"},
			Platforms:    map[string]string{"ubuntu": ">= 0.0.0"},
		}},
		{"a continuation never swallows the next call", `
depends 'apt',
depends 'yum'
name('x'
version '1.0.0'
`, CookbookMetadata{Dependencies: map[string]string{"yum": ">= 0.0.0"}, Version: "1.0.0"}},
		{"non-literal calls are not recognized", `
name File.basename(__dir__)
# version '9.9.9'
version = '9.9.9'
description "tab\tin a double-quoted string"
long_description IO.read(File.join(File.dirname(__FILE__), 'README.md'))
maintainer 'a' + 'b'
license 'MIT' if true
depends pkg
supports %w(ubuntu debian)
chef_version
name 'unterminated
name 'x' 'y'
name('x'
privacy trueish
depends :1abc
depends :
depends ::Apt
depends :'unterminated
eager_load_libraries ['a', true]
eager_load_libraries ['a' 'b']
eager_load_libraries ['a'
`, CookbookMetadata{}},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got, err := ParseMetadataRb([]byte(c.src))
			if err != nil {
				t.Fatalf("ParseMetadataRb: %v", err)
			}
			if !reflect.DeepEqual(*got, c.want) {
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
		"bad provides":           "provides 'x', '>> 1.0'\n",
		"bad supports version":   "supports 'ubuntu', '>= 1.2.3.4'\n",
		// Chef's DSL validates the type and number of literal arguments.
		"privacy string":           "privacy 'yes'\n",
		"privacy two arguments":    "privacy true, false\n",
		"name boolean":             "name true\n",
		"name two arguments":       "name 'a', 'b'\n",
		"version two arguments":    "version '1.0', '2.0'\n",
		"version boolean":          "version true\n",
		"depends boolean":          "depends 'apt', true\n",
		"depends list":             "depends ['apt']\n",
		"recipe one argument":      "recipe 'nginx::default'\n",
		"recipe three arguments":   "recipe 'a', 'b', 'c'\n",
		"recipe boolean":           "recipe 'a', false\n",
		"gem boolean":              "gem 'rack', true\n",
		"chef_version boolean":     "chef_version true\n",
		"eager_load two arguments": "eager_load_libraries true, false\n",
	}
	for desc, src := range cases {
		t.Run(desc, func(t *testing.T) {
			_, err := ParseMetadataRb([]byte(src))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "metadata.rb: line ") {
				t.Errorf("error %q should name the metadata.rb line", err)
			}
			if errors.Is(err, ErrMetadataVersionNotLiteral) {
				t.Errorf("error %q should not be ErrMetadataVersionNotLiteral", err)
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
	got, err := ParseMetadataJSON([]byte(src))
	if err != nil {
		t.Fatalf("ParseMetadataJSON: %v", err)
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
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("got  %+v\nwant %+v", *got, want)
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
			if _, err := ParseMetadataJSON([]byte(src)); err == nil {
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
		md, err := LoadCookbookMetadata(dir)
		if err != nil {
			t.Fatal(err)
		}
		if md.Name != "fromjson" || md.Version != "1.0.0" {
			t.Fatalf("got %+v", md)
		}
	})
	t.Run("metadata.rb when there is no metadata.json", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"metadata.rb": "name 'fromrb'\nversion '2.0'\n"})
		md, err := LoadCookbookMetadata(dir)
		if err != nil {
			t.Fatal(err)
		}
		if md.Name != "fromrb" || md.Version != "2.0.0" {
			t.Fatalf("got %+v", md)
		}
	})
	t.Run("no metadata is fs.ErrNotExist", func(t *testing.T) {
		md, err := LoadCookbookMetadata(t.TempDir())
		if !errors.Is(err, fs.ErrNotExist) || md != nil {
			t.Fatalf("got %+v, %v; want nil, fs.ErrNotExist", md, err)
		}
		if !strings.Contains(err.Error(), "metadata.json") || !strings.Contains(err.Error(), "metadata.rb") {
			t.Errorf("error %q should name both metadata files", err)
		}
	})
	t.Run("metadata.rb errors name the file", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"metadata.rb": "version 'x'\n"})
		_, err := LoadCookbookMetadata(dir)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "metadata.rb")+": line 1: version:") {
			t.Fatalf("err = %v, want it to name %s line 1", err, filepath.Join(dir, "metadata.rb"))
		}
	})
	t.Run("metadata.json errors name the file", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"metadata.json": "{"})
		_, err := LoadCookbookMetadata(dir)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "metadata.json")) {
			t.Fatalf("err = %v, want it to name %s", err, filepath.Join(dir, "metadata.json"))
		}
	})
	t.Run("a computed version is reported with the rest of the metadata", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"metadata.rb": "name 'x'\nversion IO.read('VERSION')\n"})
		md, err := LoadCookbookMetadata(dir)
		if !errors.Is(err, ErrMetadataVersionNotLiteral) {
			t.Fatalf("err = %v, want ErrMetadataVersionNotLiteral", err)
		}
		if md == nil || md.Name != "x" || md.Version != "" {
			t.Fatalf("md = %+v, want name x and no version", md)
		}
	})
	t.Run("metadata.json that is a directory is an error", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "metadata.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCookbookMetadata(dir); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want a read error", err)
		}
	})
}

// A version metadata.rb computes cannot be read statically. Returning the
// zero version would upload the cookbook as 0.0.0, so the parse says so.
func TestParseMetadataRb_VersionNotLiteral(t *testing.T) {
	cases := map[string]string{
		"method call":        "name 'x'\nversion IO.read(File.join(__dir__, 'VERSION')).strip\n",
		"interpolation":      "name 'x'\nversion \"#{major}.0.0\"\n",
		"parenthesized":      "name 'x'\nversion(computed)\n",
		"conditional":        "name 'x'\nversion '1.0.0' if ENV['X']\n",
		"after a literal":    "name 'x'\nversion '1.0.0'\nversion computed\n",
		"inside a block":     "name 'x'\nif true\n  version '1.0.0' + ''\nend\n",
		"over several lines": "name 'x'\nversion(\n  compute\n)\n",
	}
	for desc, src := range cases {
		t.Run(desc, func(t *testing.T) {
			md, err := ParseMetadataRb([]byte(src))
			if !errors.Is(err, ErrMetadataVersionNotLiteral) {
				t.Fatalf("err = %v, want ErrMetadataVersionNotLiteral", err)
			}
			if !strings.Contains(err.Error(), "metadata.rb: line ") {
				t.Errorf("error %q should name the metadata.rb line", err)
			}
			if md == nil || md.Name != "x" || md.Version != "" {
				t.Fatalf("md = %+v, want name x and no version", md)
			}
		})
	}
	t.Run("a hard error wins", func(t *testing.T) {
		md, err := ParseMetadataRb([]byte("version computed\ndepends 'apt', 'latest'\n"))
		if err == nil || errors.Is(err, ErrMetadataVersionNotLiteral) || md != nil {
			t.Fatalf("got %+v, %v; want nil and a constraint error", md, err)
		}
	})
}

func TestCookbookMetadata_CompiledJSON(t *testing.T) {
	t.Run("round-trips through ParseMetadataJSON", func(t *testing.T) {
		md := CookbookMetadata{
			Name: "x", Version: "1.0.0", EagerLoadLibraries: false,
			Dependencies: map[string]string{"apt": ">= 1.0"},
		}
		data, err := md.CompiledJSON()
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParseMetadataJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		if back.Name != "x" || back.Version != "1.0.0" || back.EagerLoadLibraries != false ||
			back.License != "All rights reserved" || back.Dependencies["apt"] != ">= 1.0" {
			t.Fatalf("got %+v", *back)
		}
	})
	t.Run("a metadata.json value is kept as written", func(t *testing.T) {
		// from_hash keeps the version as written, and to_h writes it back.
		md, err := ParseMetadataJSON([]byte(`{"name":"x","version":"1.2","license":"MIT"}`))
		if err != nil {
			t.Fatal(err)
		}
		data, err := md.CompiledJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"version": "1.2",`) || !strings.Contains(string(data), `"license": "MIT",`) {
			t.Fatalf("got %s", data)
		}
	})
	t.Run("Attributes and Groupings are not written", func(t *testing.T) {
		md := CookbookMetadata{Name: "x", Attributes: map[string]any{"a": 1}, Groupings: map[string]any{"g": 1}}
		data, err := md.CompiledJSON()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "attributes") || strings.Contains(string(data), "groupings") {
			t.Fatalf("got %s", data)
		}
	})
	t.Run("a name is required", func(t *testing.T) {
		if _, err := (&CookbookMetadata{Version: "1.0.0"}).CompiledJSON(); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("an unencodable value is an error", func(t *testing.T) {
		if _, err := (&CookbookMetadata{Name: "x", EagerLoadLibraries: func() {}}).CompiledJSON(); err == nil {
			t.Fatal("expected an error")
		}
	})
}
