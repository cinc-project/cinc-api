package cinc

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The golden identifiers are the ones real `chef install` produced for these
// cookbooks, as pinned by cinc-cli's resolver tests (TestComputeIdentifier-
// GoldenValues), whose fixtures testdata/identifiers copies.
func TestLocalCookbook_Identifiers(t *testing.T) {
	cases := []struct {
		dir, identifier, dotted string
	}{
		{"alpha", "3817872381e3cbf5d8bcbc16bced7e5e5438344b", "15788467879535563.69199674415168749.138943604995147"},
		{"beta", "484874ca84fbec8d44006eb2a1840781c331292f", "20345864774286316.39762740364091780.8253906954543"},
		{"gamma", "194c08b2a394d35a12d38482a060c2cdf63df9b3", "7120474658280659.25353447574511712.214189855340979"},
		// chefignore excludes *.bak and *.tmp.
		{"widget", "e80db6497ca7c1baab2f722b0372f6bc57e8bbba", ""},
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			cb, err := LocalCookbookFromDir(filepath.Join("testdata", "identifiers", c.dir), "")
			if err != nil {
				t.Fatal(err)
			}
			id, dotted := cb.Identifiers()
			if id != c.identifier {
				t.Errorf("identifier = %q, want %q", id, c.identifier)
			}
			if c.dotted != "" && dotted != c.dotted {
				t.Errorf("dotted-decimal identifier = %q, want %q", dotted, c.dotted)
			}
		})
	}
}

// cinc-cli TestChefignoreExcludesIgnoredFiles: the file set drops chefignored
// paths and keeps the chefignore itself.
func TestLocalCookbook_Files(t *testing.T) {
	dir := filepath.Join("testdata", "identifiers", "widget")
	cb, err := LocalCookbookFromDir(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var want []LocalCookbookFile
	for _, rel := range []string{"chefignore", "metadata.rb", "recipes/default.rb"} {
		disk := filepath.Join(dir, filepath.FromSlash(rel))
		data, err := os.ReadFile(disk)
		if err != nil {
			t.Fatal(err)
		}
		sum := md5.Sum(data)
		want = append(want, LocalCookbookFile{Path: rel, DiskPath: disk, Checksum: hex.EncodeToString(sum[:])})
	}
	got := cb.Files()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	got[0].Path = "changed"
	if cb.Files()[0].Path != "chefignore" {
		t.Error("Files returned the cookbook's own slice")
	}
}

// Chef sorts the fingerprint by path as Ruby compares strings, byte by byte,
// so "a.rb" (".") sorts before "a/b.rb" ("/"), although a directory walk
// visits a/ first.
func TestLocalCookbook_FilesSortByPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "x")
	writeTree(t, root, map[string]string{
		"files/a/b.rb": "1", "files/a.rb": "2", "files/a-z.rb": "3", "metadata.rb": "name 'x'\n",
	})
	cb, err := LocalCookbookFromDir(root, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range cb.Files() {
		got = append(got, f.Path)
	}
	want := []string{"files/a-z.rb", "files/a.rb", "files/a/b.rb", "metadata.rb"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// chef-cli's CookbookProfiler::Identifiers: sha1 over sorted "path:md5\n"
// lines, and the dotted form reads the hex as 14/14/12-digit integers.
func TestLocalCookbook_IdentifiersAlgorithm(t *testing.T) {
	cb := &LocalCookbook{files: []cookbookFile{
		{name: "recipes/default.rb", checksum: "22"},
		{name: "metadata.rb", checksum: "11"},
	}}
	id, dotted := cb.Identifiers()
	// printf 'metadata.rb:11\nrecipes/default.rb:22\n' | shasum
	if id != "a88abc57726d575fa4b36f76920fa630fb79da3d" {
		t.Errorf("identifier = %q", id)
	}
	if dotted != "47440337612991831.26921213363655183.182729307707965" {
		t.Errorf("dotted-decimal identifier = %q", dotted)
	}
}

// A symlink to a file inside the cookbook is uploaded under its own path
// with its target's content (Chef's loader keeps it: File.file? follows the
// link), so it counts toward the identifier the same way. cinc-cli's
// resolver skipped every symlink here, so its identifier disagreed with the
// files cinc-api uploads, and with chef-cli.
func TestLocalCookbook_IdentifiersCountInCookbookSymlinks(t *testing.T) {
	linked := filepath.Join(t.TempDir(), "x")
	writeTree(t, linked, map[string]string{"metadata.rb": "name 'x'\n", "files/real.conf": "conf\n"})
	if err := os.Symlink("real.conf", filepath.Join(linked, "files", "link.conf")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	copied := filepath.Join(t.TempDir(), "x")
	writeTree(t, copied, map[string]string{
		"metadata.rb": "name 'x'\n", "files/real.conf": "conf\n", "files/link.conf": "conf\n",
	})
	a, err := LocalCookbookFromDir(linked, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := LocalCookbookFromDir(copied, "")
	if err != nil {
		t.Fatal(err)
	}
	idA, _ := a.Identifiers()
	idB, _ := b.Identifiers()
	if idA != idB {
		t.Fatalf("symlinked cookbook identifier %s != copied cookbook identifier %s", idA, idB)
	}
}

// SkipChefignore keeps the files chefignore would drop, including a broken
// or unreadable chefignore's cookbook, and changes nothing else: root
// dot-directories, the chef-zero sentinel and out-of-cookbook symlinks are
// still skipped.
func TestLocalCookbookFromDir_SkipChefignore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "x")
	writeTree(t, root, map[string]string{
		"metadata.rb":                     "name 'x'\n",
		"chefignore":                      "*.bak\ntmp/*\n",
		"recipes/default.rb.bak":          "old\n",
		"tmp/scratch":                     "s\n",
		".git/HEAD":                       "ref\n",
		".uploaded-cookbook-version.json": "{}\n",
		"files/.hidden/keep":              "k\n",
	})
	outside := filepath.Join(t.TempDir(), "secret")
	writeTree(t, filepath.Dir(outside), map[string]string{"secret": "key\n"})
	if err := os.Symlink(outside, filepath.Join(root, "files", "leak")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	paths := func(cb *LocalCookbook) []string {
		var out []string
		for _, f := range cb.Files() {
			out = append(out, f.Path)
		}
		return out
	}

	honored, err := LocalCookbookFromDir(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(honored), []string{"chefignore", "files/.hidden/keep", "metadata.rb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default: got %v, want %v", got, want)
	}

	skipped, err := LocalCookbookFromDir(root, "", SkipChefignore())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"chefignore", "files/.hidden/keep", "metadata.rb", "recipes/default.rb.bak", "tmp/scratch"}
	if got := paths(skipped); !reflect.DeepEqual(got, want) {
		t.Fatalf("SkipChefignore: got %v, want %v", got, want)
	}
	idHonored, _ := honored.Identifiers()
	idSkipped, _ := skipped.Identifiers()
	if idHonored == idSkipped {
		t.Error("identifiers should differ once chefignored files are included")
	}
}
