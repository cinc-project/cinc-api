package cinc

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseChefignoreSkipsCommentsAndBlanks(t *testing.T) {
	patterns := parseChefignore([]byte("# a comment\n\n*.bak\n  spec/* \n\n# another\nBerksfile.lock\n"))
	want := []string{"*.bak", "spec/*", "Berksfile.lock"}
	if !slices.Equal(patterns, want) {
		t.Fatalf("patterns = %v, want %v", patterns, want)
	}
}

func TestLoadChefignoreMissingFileIgnoresNothing(t *testing.T) {
	ci, err := LoadChefignore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadChefignore: %v", err)
	}
	if ci == nil {
		t.Fatal("LoadChefignore returned nil for missing file, want empty Chefignore")
	}
	if ci.Patterns() != nil {
		t.Fatalf("Patterns() = %v, want nil for missing file", ci.Patterns())
	}
	if ci.Ignores("anything.rb") {
		t.Fatal("a missing chefignore should ignore nothing")
	}
}

func TestLoadChefignoreReadsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chefignore"), []byte("*.bak\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ci, err := LoadChefignore(dir)
	if err != nil {
		t.Fatalf("LoadChefignore: %v", err)
	}
	if !slices.Equal(ci.Patterns(), []string{"*.bak"}) {
		t.Fatalf("Patterns() = %v, want [*.bak]", ci.Patterns())
	}
}

func TestChefignoreIgnoresByBasename(t *testing.T) {
	ci := &Chefignore{patterns: []string{"*.bak"}}
	cases := map[string]bool{
		"recipes/default.rb":  false,
		"recipes/default.bak": true,
		"deep/nested/foo.bak": true,
		"foo.bak":             true,
		"foobak":              false,
	}
	for relPath, want := range cases {
		if got := ci.Ignores(relPath); got != want {
			t.Errorf("%q: got %v, want %v", relPath, got, want)
		}
	}
}

func TestChefignoreIgnoresByDirectorySegment(t *testing.T) {
	// Chef tests each pattern against the cookbook-relative path only, and
	// Ruby's `*` crosses "/" when no FNM_PATHNAME flag is given, so `spec/*`
	// excludes everything nested beneath spec/. A bare name like `.kitchen`
	// matches only that exact path, not files inside it.
	ci := &Chefignore{patterns: []string{"spec/*", ".kitchen"}}
	cases := map[string]bool{
		"spec/foo_spec.rb":        true,
		"spec/fixtures/sample.rb": true,
		".kitchen":                true,
		".kitchen/state.yml":      false,
		"recipes/default.rb":      false,
		"libraries/helper.rb":     false,
	}
	for relPath, want := range cases {
		if got := ci.Ignores(relPath); got != want {
			t.Errorf("%q: got %v, want %v", relPath, got, want)
		}
	}
}

// TestFnmatchMatchesRuby pins fnmatch to Ruby's File.fnmatch? with no flags,
// which is what Chef::Cookbook::Chefignore#ignored? calls. Every expectation
// below was produced by running File.fnmatch?(pattern, path) under Ruby.
func TestFnmatchMatchesRuby(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"Vagrantfile", "files/default/Vagrantfile", false},
		{"README*", "templates/default/README.md.erb", false},
		{"README*", "README.md", true},
		{".kitchen", ".kitchen/x", false},
		{"[!a]*.txt", "b.txt", true},
		{"[!a]*.txt", "a.txt", false},
		{"[^a]*.txt", "b.txt", true},
		{"spec/*", "spec/a/b_spec.rb", true},
		{"*~", ".foo~", false},
		{"*~", "foo~", true},
		{"*~", "dir/.foo~", true},
		{"?foo", ".foo", false},
		{"[.]foo", ".foo", false},
		{".*", ".rubocop.yml", true},
		{"*.bak", "a/b.bak", true},
		{"*/.svn/*", "a/.svn/b", true},
		{"**/*.bak", "x.bak", false},
		{"**/*.bak", "a/x.bak", true},
		{`\*x`, "*x", true},
		{`\*x`, "ax", false},
		{`\.foo`, ".foo", true},
		{"[abc", "[abc", false},
		{"a[b-d]e", "ace", true},
		{"a[]]b", "a]b", false},
		{"a[!]]b", "a]b", false},
		{"a[!]]b", "acb", false},
		{"{a,b}", "a", false},
		{"A*", "abc", false},
		{"tmp", "tmp/x", false},
		{"*/tmp/*", "a/tmp/x", true},
		{"test/*", "test/x/y", true},
		{"a?b", "a/b", true},
		{"[a-]", "-", true},
		{"[z-a]", "m", false},
		{`a\`, `a\`, false},
		{"*", "a/b", true},
		{"foo/**", "foo/a/b", true},
		{"[[:alpha:]]", "a", false},
		{"a*", "a", true},
		{"*x", "", false},
		{"?", "", false},
		{"[a]", "", false},
		{"ab", "a", false},
		{"a", "ab", false},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXbY", false},
		{"[é]x", "éx", true},
		{`[\]]x`, "]x", true},
		{`a[b-\d]`, "ac", true},
		{"[a-", "a", false},
		{`[\`, `\`, false},
		{"[a-c", "b", false},
		{"a[", "ab", false},
		{`a[a-\`, "ab", false},
	}
	for _, c := range cases {
		if got := fnmatch(c.pattern, c.path); got != c.want {
			t.Errorf("fnmatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestLoadChefignoreFindsParentFile(t *testing.T) {
	// In a chef-repo the chefignore lives in cookbooks/, above each cookbook;
	// Chef walks up from the cookbook directory to find it.
	repo := t.TempDir()
	cb := filepath.Join(repo, "cookbooks", "nginx")
	if err := os.MkdirAll(cb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cookbooks", "chefignore"), []byte("spec/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ci, err := LoadChefignore(cb)
	if err != nil {
		t.Fatalf("LoadChefignore: %v", err)
	}
	if !slices.Equal(ci.Patterns(), []string{"spec/*"}) {
		t.Fatalf("Patterns() = %v, want [spec/*]", ci.Patterns())
	}
}

func TestLoadChefignoreNearestFileWins(t *testing.T) {
	repo := t.TempDir()
	cb := filepath.Join(repo, "nginx")
	if err := os.MkdirAll(cb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "chefignore"), []byte("outer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cb, "chefignore"), []byte("inner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ci, err := LoadChefignore(cb)
	if err != nil {
		t.Fatalf("LoadChefignore: %v", err)
	}
	if !slices.Equal(ci.Patterns(), []string{"inner"}) {
		t.Fatalf("Patterns() = %v, want [inner]; the files are not merged", ci.Patterns())
	}
}

func TestLoadChefignoreRelativeDirAscendsPastCwd(t *testing.T) {
	// A relative cookbook path must still find a chefignore above the working
	// directory, as Chef's loader does with its expanded cookbook path.
	repo := t.TempDir()
	cb := filepath.Join(repo, "nginx")
	if err := os.MkdirAll(cb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "chefignore"), []byte("*.bak\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cb)
	ci, err := LoadChefignore(".")
	if err != nil {
		t.Fatalf("LoadChefignore: %v", err)
	}
	if !slices.Equal(ci.Patterns(), []string{"*.bak"}) {
		t.Fatalf("Patterns() = %v, want [*.bak]", ci.Patterns())
	}
}

func TestLoadChefignoreNonFileIgnoresNothing(t *testing.T) {
	// Chef stops at the first path named chefignore; if that is not a regular
	// file it ignores nothing rather than continuing upward.
	repo := t.TempDir()
	cb := filepath.Join(repo, "nginx")
	if err := os.MkdirAll(filepath.Join(cb, "chefignore"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "chefignore"), []byte("*.bak\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ci, err := LoadChefignore(cb)
	if err != nil {
		t.Fatalf("LoadChefignore: %v", err)
	}
	if ci.Patterns() != nil {
		t.Fatalf("Patterns() = %v, want nil", ci.Patterns())
	}
}

func TestLoadChefignoreUnreadableFileErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read any file")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "chefignore")
	if err := os.WriteFile(p, []byte("*.bak\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChefignore(dir); err == nil {
		t.Fatal("expected an error for an unreadable chefignore")
	}
}

func TestLoadChefignoreSkipsNonDirectories(t *testing.T) {
	// Chef ascends only through directories, so a cookbook path that is a
	// file or does not exist still finds the chefignore above it.
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "chefignore"), []byte("*.bak\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, "metadata.rb")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{file, filepath.Join(repo, "absent")} {
		ci, err := LoadChefignore(dir)
		if err != nil {
			t.Fatalf("LoadChefignore(%s): %v", dir, err)
		}
		if !slices.Equal(ci.Patterns(), []string{"*.bak"}) {
			t.Fatalf("LoadChefignore(%s).Patterns() = %v, want [*.bak]", dir, ci.Patterns())
		}
	}
}

func TestLoadChefignoreStatErrors(t *testing.T) {
	// A chefignore that cannot even be stat'ed (its directory is not
	// searchable) is an error, not silently treated as absent.
	if os.Getuid() == 0 {
		t.Skip("root can search any directory")
	}
	dir := t.TempDir()
	cb := filepath.Join(dir, "nginx")
	if err := os.Mkdir(cb, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cb, 0o755) })
	if _, err := LoadChefignore(cb); err == nil {
		t.Fatal("expected an error when the chefignore cannot be stat'ed")
	}
}

func TestChefignorePrunes(t *testing.T) {
	ci := &Chefignore{patterns: []string{"spec/*", "tmp", `lit\*`, "*.bak"}}
	cases := map[string]bool{
		"spec":    true,  // spec/* matches spec/ and so everything under it
		"tmp":     false, // tmp matches the path tmp, not tmp/x
		"lit*":    false, // an escaped trailing * is a literal
		"recipes": false,
		"a.bak":   false, // *.bak does not match "a.bak/"
		"specfoo": false,
	}
	for dir, want := range cases {
		if got := ci.prunes(dir); got != want {
			t.Errorf("prunes(%q) = %v, want %v", dir, got, want)
		}
	}
	var nilCI *Chefignore
	if nilCI.prunes("spec") {
		t.Error("a nil Chefignore should prune nothing")
	}
}
func TestChefignoreIgnoresFullPath(t *testing.T) {
	ci := &Chefignore{patterns: []string{"recipes/default.bak"}}
	if !ci.Ignores("recipes/default.bak") {
		t.Fatal("expected full-path match")
	}
	if ci.Ignores("recipes/other.bak") {
		t.Fatal("did not expect match for a different file")
	}
}

func TestChefignoreEmptyIgnoresNothing(t *testing.T) {
	if (&Chefignore{}).Ignores("anything.rb") {
		t.Fatal("an empty Chefignore should not match")
	}
	var nilCI *Chefignore
	if nilCI.Ignores("anything.rb") {
		t.Fatal("a nil Chefignore should not match")
	}
	ci := &Chefignore{patterns: []string{"*.bak"}}
	if ci.Ignores("") || ci.Ignores(".") {
		t.Fatal(`empty path and "." should never be ignored`)
	}
}
