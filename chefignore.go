package cinc

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Chefignore holds the glob patterns parsed from a `chefignore` file and
// reports whether a cookbook-relative path should be excluded from upload or
// packaging. It mirrors Chef::Cookbook::Chefignore: cookbook uploads, cookbook
// archives, and Policyfile content-identifier computation all match files
// through one Chefignore so they agree on exactly which files belong to a
// cookbook.
type Chefignore struct {
	patterns []string
}

// LoadChefignore finds the chefignore that applies to cookbookDir and returns
// its parsed patterns. Like Chef, it looks in cookbookDir first and then in
// each parent directory up to the filesystem root, using the first file named
// `chefignore` it finds; files are never merged. That covers both a standalone
// cookbook and a chef-repo, whose single chefignore lives in `cookbooks/`.
//
// Finding no chefignore is not an error — the returned Chefignore ignores
// nothing, as it does when the first `chefignore` found is not a regular file.
func LoadChefignore(cookbookDir string) (*Chefignore, error) {
	dir, err := filepath.Abs(cookbookDir)
	if err != nil {
		return nil, err
	}
	for ; ; dir = filepath.Dir(dir) {
		// Like Chef, look only in directories: a cookbookDir that is missing
		// or is a file is skipped in favour of its parents.
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			found, ci, err := readChefignore(filepath.Join(dir, "chefignore"))
			if found || err != nil {
				return ci, err
			}
		}
		if filepath.Dir(dir) == dir {
			return &Chefignore{}, nil
		}
	}
}

// readChefignore loads the chefignore at p. found is false when no such path
// exists; a path that exists but is not a regular file ignores nothing.
func readChefignore(p string) (found bool, ci *Chefignore, err error) {
	fi, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil, nil
	case err != nil:
		return true, nil, err
	case !fi.Mode().IsRegular():
		return true, &Chefignore{}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return true, nil, err
	}
	return true, &Chefignore{patterns: parseChefignore(data)}, nil
}

// parseChefignore extracts the glob patterns from a chefignore file's bytes,
// dropping blank lines and comments (lines whose first non-space character is
// "#"). Surrounding whitespace is trimmed from each pattern.
func parseChefignore(data []byte) []string {
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// Patterns returns the chefignore globs, in file order. The result is a copy;
// mutating it does not affect the Chefignore.
func (c *Chefignore) Patterns() []string {
	if c == nil || len(c.patterns) == 0 {
		return nil
	}
	return append([]string(nil), c.patterns...)
}

// Ignores reports whether relPath — a forward-slash, cookbook-relative file
// path — is excluded by any chefignore pattern. It matches Chef exactly: each
// pattern is tested against the whole relative path with Ruby's
// File.fnmatch? and no flags. So `*` and `?` also match "/" (`spec/*` excludes
// everything under spec/, `*.bak` excludes a .bak file at any depth), a
// wildcard never matches a leading "." of the path, and a pattern is never
// tested against a basename or an ancestor directory on its own (`Vagrantfile`
// excludes only the root Vagrantfile, not files/default/Vagrantfile).
//
// A nil receiver, no patterns, or an empty/"." path all ignore nothing.
func (c *Chefignore) Ignores(relPath string) bool {
	if c == nil || relPath == "" || relPath == "." {
		return false
	}
	for _, pattern := range c.patterns {
		if fnmatch(pattern, relPath) {
			return true
		}
	}
	return false
}

// prunes reports whether every file beneath the directory relDir is ignored,
// so a walk need not descend into it. Chef itself only filters files; this is
// purely a shortcut and must never exclude a file Ignores would keep. It holds
// for a pattern ending in an unescaped `*` that matches relDir + "/": whatever
// prefix of that string the rest of the pattern matched, the trailing `*`
// absorbs any continuation, and the leading-"." rule depends only on the
// path's first character, which a continuation does not change.
func (c *Chefignore) prunes(relDir string) bool {
	if c == nil {
		return false
	}
	for _, pattern := range c.patterns {
		if strings.HasSuffix(pattern, "*") && !escapedAt(pattern, len(pattern)-1) && fnmatch(pattern, relDir+"/") {
			return true
		}
	}
	return false
}

// escapedAt reports whether the byte at index i of pattern is preceded by an
// odd number of backslashes, i.e. is a literal rather than a metacharacter.
func escapedAt(pattern string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && pattern[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

// fnmatch reports whether name matches pattern under Ruby's File.fnmatch? with
// no flags, which is what Chef::Cookbook::Chefignore#ignored? calls. It is a
// port of fnmatch_helper and bracket from Ruby's dir.c with FNM_PATHNAME,
// FNM_DOTMATCH, FNM_NOESCAPE, FNM_CASEFOLD and FNM_EXTGLOB all unset:
//
//   - `*` matches any run of characters and `?` any one character, "/"
//     included;
//   - neither wildcards nor a bracket expression match a "." at the very start
//     of name;
//   - `[...]` is a character class, negated by a leading `!` or `^`, with
//     `a-z` ranges; an unterminated class matches nothing, and `[]` is an
//     empty class rather than a literal "]";
//   - a backslash makes the next character literal;
//   - `{a,b}` and `[[:alpha:]]` have no special meaning.
//
// Go's path.Match differs on every point except the first half of the first,
// which is why it is not used.
func fnmatch(pattern, name string) bool {
	p, s := []rune(pattern), []rune(name)
	// unescape returns the index of the character at p[i], skipping a
	// backslash that escapes it.
	unescape := func(i int) int {
		if i < len(p) && p[i] == '\\' {
			return i + 1
		}
		return i
	}
	if len(s) > 0 && s[0] == '.' {
		if j := unescape(0); j >= len(p) || p[j] != '.' {
			return false
		}
	}
	pi, si := 0, 0
	pStar, sStar := -1, -1 // resume points after the most recent `*`
	for {
		if pi < len(p) {
			switch p[pi] {
			case '*':
				for pi < len(p) && p[pi] == '*' {
					pi++
				}
				if unescape(pi) >= len(p) {
					return true
				}
				if si >= len(s) {
					return false
				}
				pStar, sStar = pi, si
				continue
			case '?':
				if si >= len(s) {
					return false
				}
				pi++
				si++
				continue
			case '[':
				if si >= len(s) {
					return false
				}
				if next, ok := bracket(p, pi+1, s[si]); ok {
					pi = next
					si++
					continue
				}
				goto failed
			}
		}
		// An ordinary (possibly escaped) character.
		pi = unescape(pi)
		if si >= len(s) {
			return pi >= len(p)
		}
		if pi < len(p) && p[pi] == s[si] {
			pi++
			si++
			continue
		}
	failed:
		// Let the most recent `*` absorb one more character and retry.
		if pStar < 0 {
			return false
		}
		sStar++
		pi, si = pStar, sStar
	}
}

// bracket matches c against the character class starting at p[i] (just after
// the opening "["). It returns the index just past the closing "]" and
// whether c is in the class. An unterminated class never matches.
func bracket(p []rune, i int, c rune) (int, bool) {
	if i >= len(p) {
		return 0, false
	}
	negate := p[i] == '!' || p[i] == '^'
	if negate {
		i++
	}
	in := false
	for i >= len(p) || p[i] != ']' {
		if i >= len(p) {
			return 0, false
		}
		if p[i] == '\\' {
			i++
		}
		if i >= len(p) {
			return 0, false
		}
		lo := p[i]
		i++
		if i >= len(p) {
			return 0, false
		}
		if p[i] == '-' && i+1 < len(p) && p[i+1] != ']' {
			i++
			if p[i] == '\\' {
				i++
			}
			if i >= len(p) {
				return 0, false
			}
			hi := p[i]
			i++
			if lo <= c && c <= hi || c == lo || c == hi {
				in = true
			}
			continue
		}
		if c == lo {
			in = true
		}
	}
	if in == negate {
		return 0, false
	}
	return i + 1, true
}
