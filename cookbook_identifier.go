package cinc

import (
	"crypto/sha1" //nolint:gosec // chef-cli's content identifier is defined as SHA-1
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

// LocalCookbookFile is one file of a LocalCookbook: exactly the files an
// upload sends and the Policyfile identifier covers.
type LocalCookbookFile struct {
	// Path is the file's path relative to the cookbook root, with forward
	// slashes (e.g. recipes/default.rb). It is the path the file is uploaded,
	// archived and fingerprinted under.
	Path string
	// DiskPath is where the file is read from. For a symlink inside the
	// cookbook it is the link, not its target.
	DiskPath string
	// Checksum is the hex MD5 of the file's content when the cookbook was
	// loaded.
	Checksum string
}

// Files returns the cookbook's files, sorted by Path (byte order), as
// LocalCookbookFromDir selected them; see there for the rules. Callers that
// archive, copy or fingerprint a cookbook should use this set, so that what
// they produce matches what an upload sends. The slice is a copy.
func (cb *LocalCookbook) Files() []LocalCookbookFile {
	out := make([]LocalCookbookFile, len(cb.files))
	for i, f := range cb.files {
		out[i] = LocalCookbookFile{Path: f.name, DiskPath: f.path, Checksum: f.checksum}
	}
	return out
}

// Identifiers returns the cookbook's Policyfile content identifier and its
// dotted-decimal form, as chef-cli's CookbookProfiler::Identifiers computes
// them for a Policyfile.lock.json: identifier is the SHA-1 hex of one
// "path:md5\n" line per file, sorted by path; dottedDecimal reads that hex as
// three integers of 14, 14 and 12 digits joined by "." (the
// cookbook_artifacts slug older Chef Servers need). The identifier is
// computed over Files, so it always describes the files an upload of cb
// sends.
func (cb *LocalCookbook) Identifiers() (identifier, dottedDecimal string) {
	files := slices.Clone(cb.files)
	slices.SortFunc(files, func(a, b cookbookFile) int { return strings.Compare(a.name, b.name) })
	h := sha1.New() //nolint:gosec // see import
	for _, f := range files {
		h.Write([]byte(f.name + ":" + f.checksum + "\n"))
	}
	identifier = hex.EncodeToString(h.Sum(nil))
	parts := make([]string, 3)
	for i, span := range [][2]int{{0, 14}, {14, 28}, {28, 40}} {
		// 14 hex digits fit in 56 bits, so this cannot fail.
		n, _ := strconv.ParseUint(identifier[span[0]:span[1]], 16, 64)
		parts[i] = strconv.FormatUint(n, 10)
	}
	return identifier, strings.Join(parts, ".")
}
