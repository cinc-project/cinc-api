package cinc

import "net/url"

// esc percent-encodes a caller-supplied identifier so it occupies exactly one
// path segment.
//
// Two things depend on this. The v1.3 signature covers the canonical request
// path, and net/http re-derives the wire path from the parsed URL — so an
// unescaped name that Go encodes differently (a space, a non-ASCII rune) is
// signed one way and sent another, and the server rejects it with a 401. And a
// name containing "/" or ".." would otherwise walk out of the collection its
// service owns, producing a correctly-signed request against a different
// object entirely.
//
// Every identifier Chef itself considers legal is unreserved, so for valid
// input this is the identity function and nothing on the wire changes.
func esc(s string) string { return url.PathEscape(s) }
