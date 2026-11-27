// Package cinctest provides an httptest-based fake Chef server for tests.
package cinctest

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/cinc-project/cinc-api/internal/signing"
)

// KeyFile is the fixture key every test client signs with, relative to the
// package directory a test runs in.
const KeyFile = "testdata/test_key.pem"

// Route maps "METHOD /path" to a handler.
type Route struct {
	Status int
	Body   string
	// Assert, if set, runs against the incoming request.
	Assert func(t *testing.T, r *http.Request, body []byte)
}

// Server is a fake Chef server backed by httptest.
type Server struct {
	*httptest.Server
	t      *testing.T
	routes map[string]Route
	pub    *rsa.PublicKey
}

// New creates a Server. Register routes with Handle before use.
//
// Every request it receives must carry a valid Chef v1.3 signature by the
// KeyFile key over the path as it arrived on the wire, which is what erchef
// verifies; a request that does not fails the test.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{t: t, routes: map[string]Route{}, pub: loadPublicKey(t)}
	s.Server = httptest.NewServer(http.HandlerFunc(s.dispatch))
	t.Cleanup(s.Close)
	return s
}

// Handle registers a route, e.g. Handle("GET /organizations/o/nodes/x", route).
func (s *Server) Handle(key string, r Route) { s.routes[key] = r }

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.Header.Get("X-Ops-Authorization-1") == "" {
		s.t.Errorf("cinctest: unsigned request %s %s", r.Method, r.URL.Path)
	} else if err := s.verify(r, body); err != nil {
		s.t.Errorf("cinctest: %s %s: signature does not verify over the wire path %q: %v",
			r.Method, r.URL.Path, r.URL.EscapedPath(), err)
	}
	route, ok := s.routes[r.Method+" "+r.URL.Path]
	if !ok {
		s.t.Errorf("cinctest: no route for %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	if route.Assert != nil {
		route.Assert(s.t, r, body)
	}
	status := route.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, route.Body) // canned test response; the assertion catches a short write
}

// verify checks r's signature the way erchef does: over the escaped wire
// path, the body's hash, and the API version the request asked for.
func (s *Server) verify(r *http.Request, body []byte) error {
	var sig bytes.Buffer
	for i := 1; ; i++ {
		chunk := r.Header.Get("X-Ops-Authorization-" + strconv.Itoa(i))
		if chunk == "" {
			break
		}
		sig.WriteString(chunk)
	}
	raw, err := base64.StdEncoding.DecodeString(sig.String())
	if err != nil {
		return err
	}
	canonical := signing.CanonicalRequest(signing.Request{
		Method:     r.Method,
		Path:       r.URL.EscapedPath(),
		Body:       body,
		UserID:     r.Header.Get("X-Ops-Userid"),
		Timestamp:  r.Header.Get("X-Ops-Timestamp"),
		APIVersion: r.Header.Get("X-Ops-Server-API-Version"),
	})
	digest := sha256.Sum256([]byte(canonical))
	return rsa.VerifyPKCS1v15(s.pub, crypto.SHA256, digest[:], raw)
}

func loadPublicKey(t *testing.T) *rsa.PublicKey {
	t.Helper()
	data, err := os.ReadFile(KeyFile)
	if err != nil {
		t.Fatalf("cinctest: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("cinctest: %s holds no PEM block", KeyFile)
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return &k.PublicKey
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("cinctest: parse %s: %v", KeyFile, err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("cinctest: %s is not an RSA key", KeyFile)
	}
	return &rk.PublicKey
}
