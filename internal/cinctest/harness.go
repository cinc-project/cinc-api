// Package cinctest provides an httptest-based fake Chef server for tests.
package cinctest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
}

// New creates a Server. Register routes with Handle before use.
func New(t *testing.T) *Server {
	s := &Server{t: t, routes: map[string]Route{}}
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
