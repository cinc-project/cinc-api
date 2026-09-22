// Package signing implements the Chef v1.3 (SHA-256) signed-header protocol.
package signing

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// ServerAPIVersion is the X-Ops-Server-API-Version the client requests by
// default (see Request.APIVersion).
const ServerAPIVersion = "1"

// Request is the minimal set of fields needed to sign an HTTP request.
type Request struct {
	Method    string // upper-case HTTP method
	Path      string // request path, will be canonicalized
	Body      []byte // request body, may be nil
	UserID    string // Chef client/user name
	Timestamp string // ISO-8601 UTC, e.g. 2024-01-01T00:00:00Z
	// APIVersion is the X-Ops-Server-API-Version to request and sign; empty
	// means ServerAPIVersion.
	APIVersion string
}

// apiVersion returns the server API version r requests.
func (r Request) apiVersion() string {
	if r.APIVersion == "" {
		return ServerAPIVersion
	}
	return r.APIVersion
}

// ContentHash returns base64(sha256(body)).
func ContentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// CanonicalPath collapses repeated slashes and strips a trailing slash.
func CanonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	// Fast path: real request paths almost never contain a repeated slash,
	// so avoid the allocating scan and just trim a trailing slash.
	if !strings.Contains(p, "//") {
		if len(p) > 1 && p[len(p)-1] == '/' {
			return p[:len(p)-1]
		}
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	var prev byte
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' && prev == '/' {
			continue
		}
		b.WriteByte(c)
		prev = c
	}
	out := b.String()
	if len(out) > 1 && out[len(out)-1] == '/' {
		out = out[:len(out)-1]
	}
	return out
}

// CanonicalRequest builds the v1.3 string-to-sign for r.
func CanonicalRequest(r Request) string {
	return canonicalRequest(r, ContentHash(r.Body))
}

// canonicalRequest builds the v1.3 string-to-sign using a precomputed content
// hash, so callers that also need the hash for a header (SignHeaders) can hash
// the body just once instead of twice.
func canonicalRequest(r Request, contentHash string) string {
	var b strings.Builder
	// "Method:" + ... and the six fixed-string separators total ~90 bytes.
	b.Grow(len(r.Method) + len(r.Path) + len(contentHash) +
		len(r.Timestamp) + len(r.UserID) + 96)
	b.WriteString("Method:")
	b.WriteString(r.Method)
	b.WriteString("\nPath:")
	b.WriteString(CanonicalPath(r.Path))
	b.WriteString("\nX-Ops-Content-Hash:")
	b.WriteString(contentHash)
	b.WriteString("\nX-Ops-Sign:version=1.3\nX-Ops-Timestamp:")
	b.WriteString(r.Timestamp)
	b.WriteString("\nX-Ops-UserId:")
	b.WriteString(r.UserID)
	b.WriteString("\nX-Ops-Server-API-Version:")
	b.WriteString(r.apiVersion())
	return b.String()
}
