package cinc

import (
	"errors"
	"reflect"
	"testing"
)

// The tests in this file port the metadata cases of cinc-cli's own metadata.rb
// reader (cli/cookbook, built on a Ruby AST), so the CLI can drop it in favor
// of this package. Where the two disagreed, the expectation follows
// Chef::Cookbook::Metadata and the comment says what the CLI did.

// cinc-cli TestLoadMetadataJSONGeneratesFromMetadataRB.
func TestMetadataCompat_EveryField(t *testing.T) {
	src := `
name 'sample'
maintainer 'Sous Chefs'
maintainer_email 'help@sous-chefs.org'
license 'Apache-2.0'
description 'Sample cookbook'
long_description 'Longer text'
version '1.2.3'
source_url 'https://example.test/source'
issues_url 'https://example.test/issues'
chef_version '>= 16'
ohai_version '>= 17'
supports :ubuntu, '>= 20.04'
supports 'debian'
depends 'apt', '~> 7.0'
provides 'sample::default'
recipe 'sample::default', 'Configures sample'
gem 'rack', '>= 2'
privacy true
eager_load_libraries ['helpers.rb']
`
	got, err := ParseMetadataRb([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := CookbookMetadata{
		Name: "sample", Version: "1.2.3", Description: "Sample cookbook",
		LongDescription: "Longer text", Maintainer: "Sous Chefs",
		MaintainerEmail: "help@sous-chefs.org", License: "Apache-2.0",
		SourceURL: "https://example.test/source", IssuesURL: "https://example.test/issues",
		Privacy:      true,
		Platforms:    map[string]string{"ubuntu": ">= 20.04", "debian": ">= 0.0.0"},
		Dependencies: map[string]string{"apt": "~> 7.0"},
		Providing:    map[string]string{"sample::default": ">= 0.0.0"},
		Recipes:      map[string]string{"sample::default": "Configures sample"},
		ChefVersions: [][]string{{">= 16"}},
		OhaiVersions: [][]string{{">= 17"}},
		Gems:         [][]string{{"rack", ">= 2"}},

		EagerLoadLibraries: []string{"helpers.rb"},
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("got  %+v\nwant %+v", *got, want)
	}
}

// The CLI stored what metadata.rb wrote; Chef stores normalized values.
func TestMetadataCompat_Normalization(t *testing.T) {
	got, err := ParseMetadataRb([]byte(`
version '1.2'
depends 'apt', '1.2'
chef_version '16'
`))
	if err != nil {
		t.Fatal(err)
	}
	// CLI: "1.2", "1.2" and "16". Chef::Version#to_s pads the version,
	// VersionConstraint#to_s spells out "=", and Gem::Requirement does too.
	if got.Version != "1.2.0" || got.Dependencies["apt"] != "= 1.2" ||
		!reflect.DeepEqual(got.ChefVersions, [][]string{{"= 16"}}) {
		t.Fatalf("got %+v", *got)
	}
}

// The CLI silently dropped a self-dependency. Chef's depends raises
// ("Cookbook depends on itself"), so knife could not upload the cookbook.
func TestMetadataCompat_SelfDependency(t *testing.T) {
	if _, err := ParseMetadataRb([]byte("name 'apt'\ndepends 'apt'\n")); err == nil {
		t.Fatal("expected a self-dependency error")
	}
}

// cinc-cli TestLoadMetadataReportsUnsupportedRubyExpressions. The CLI failed
// the whole parse on any argument that was not a literal. Chef evaluates the
// Ruby, so the call works there; a static reader cannot see its value and
// skips it. The version alone is reported, since without it the cookbook
// would upload as 0.0.0.
func TestMetadataCompat_NonLiteralArgument(t *testing.T) {
	got, err := ParseMetadataRb([]byte("name 'nginx'\nversion '1.2.0'\nplatform = 'ubuntu'\nsupports platform\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "nginx" || got.Version != "1.2.0" || got.Platforms != nil {
		t.Fatalf("got %+v", *got)
	}
}

// cinc-cli TestReadVersionAcceptsLiteralMetadataRBVersion and the upload guard
// ReadVersion provided: a literal version is read, a computed one is an error
// the caller can recognize.
func TestMetadataCompat_UploadVersionGuard(t *testing.T) {
	md, err := ParseMetadataRb([]byte("name 'nginx'\nversion '1.2.0'\n"))
	if err != nil || md.Version != "1.2.0" {
		t.Fatalf("got %+v, %v", md, err)
	}
	_, err = ParseMetadataRb([]byte("name 'nginx'\nversion File.read('VERSION')\n"))
	if !errors.Is(err, ErrMetadataVersionNotLiteral) {
		t.Fatalf("err = %v, want ErrMetadataVersionNotLiteral", err)
	}
	// The CLI's guard also refused a metadata.rb with no version at all;
	// Chef uploads that as 0.0.0, so it is not an error here.
	md, err = ParseMetadataRb([]byte("name 'nginx'\n"))
	if err != nil || md.Version != "" {
		t.Fatalf("got %+v, %v", md, err)
	}
}

// The CLI rejected a privacy argument that was not a boolean, as Chef's
// set_or_return does.
func TestMetadataCompat_PrivacyType(t *testing.T) {
	if _, err := ParseMetadataRb([]byte("privacy 'yes'\n")); err == nil {
		t.Fatal("expected an error")
	}
}

// cinc-cli's LoadMetadata compiled a metadata.rb-only cookbook to the
// metadata.json that `cinc supermarket share` packs, filling Chef's defaults.
// wantCompiledMinimal is byte-for-byte what it produced for "name 'minimal'".
const wantCompiledMinimal = `{
  "name": "minimal",
  "description": "",
  "long_description": "",
  "maintainer": "",
  "maintainer_email": "",
  "license": "All rights reserved",
  "platforms": {},
  "dependencies": {},
  "providing": {},
  "recipes": {},
  "version": "0.0.0",
  "source_url": "",
  "issues_url": "",
  "privacy": false,
  "chef_versions": [],
  "ohai_versions": [],
  "gems": [],
  "eager_load_libraries": true
}
`

func TestMetadataCompat_CompiledJSONDefaults(t *testing.T) {
	md, err := ParseMetadataRb([]byte("name 'minimal'\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := md.CompiledJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantCompiledMinimal {
		t.Fatalf("got\n%s\nwant\n%s", got, wantCompiledMinimal)
	}
}

// For every field set, the CLI produced this document except where noted:
// it dropped `privacy true` (writing false) and `eager_load_libraries [...]`
// (writing true), and escaped <, > and & as <, > and &, which
// Chef's encoder does not. Map keys are sorted; Chef keeps declaration order,
// which no JSON reader depends on.
const wantCompiledEveryField = `{
  "name": "sample",
  "description": "Sample cookbook",
  "long_description": "Longer text",
  "maintainer": "Sous Chefs <help@sous-chefs.org>",
  "maintainer_email": "help@sous-chefs.org",
  "license": "Apache-2.0",
  "platforms": {
    "debian": ">= 0.0.0",
    "ubuntu": ">= 20.04"
  },
  "dependencies": {
    "apt": "~> 7.0"
  },
  "providing": {
    "sample::default": ">= 0.0.0"
  },
  "recipes": {
    "sample::default": "Configures sample"
  },
  "version": "1.2.3",
  "source_url": "https://example.test/source?a=1&b=2",
  "issues_url": "https://example.test/issues",
  "privacy": true,
  "chef_versions": [
    [
      ">= 16"
    ]
  ],
  "ohai_versions": [
    [
      ">= 17"
    ]
  ],
  "gems": [
    [
      "rack",
      ">= 2"
    ]
  ],
  "eager_load_libraries": [
    "helpers.rb"
  ]
}
`

func TestMetadataCompat_CompiledJSONEveryField(t *testing.T) {
	md, err := ParseMetadataRb([]byte(`
name 'sample'
maintainer 'Sous Chefs <help@sous-chefs.org>'
maintainer_email 'help@sous-chefs.org'
license 'Apache-2.0'
description 'Sample cookbook'
long_description 'Longer text'
version '1.2.3'
source_url 'https://example.test/source?a=1&b=2'
issues_url 'https://example.test/issues'
chef_version '>= 16'
ohai_version '>= 17'
supports :ubuntu, '>= 20.04'
supports 'debian'
depends 'apt', '~> 7.0'
provides 'sample::default'
recipe 'sample::default', 'Configures sample'
gem 'rack', '>= 2'
privacy true
eager_load_libraries ['helpers.rb']
`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := md.CompiledJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantCompiledEveryField {
		t.Fatalf("got\n%s\nwant\n%s", got, wantCompiledEveryField)
	}
}
