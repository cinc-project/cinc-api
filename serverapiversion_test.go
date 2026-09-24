package cinc

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func responseWithVersion(header string) *Response {
	h := http.Header{}
	if header != "" {
		h.Set("X-Ops-Server-API-Version", header)
	}
	return &Response{HTTPResponse: &http.Response{Header: h}}
}

func TestResponse_ServerAPIVersion(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   ServerAPIVersion
		ok     bool
	}{
		{
			// erchef and cinc-server-ng both encode every value as a string.
			name:   "chef server header",
			header: `{"min_version":"0","max_version":"2","request_version":"1","response_version":"1"}`,
			want:   ServerAPIVersion{Min: 0, Max: 2, Request: 1, Response: 1},
			ok:     true,
		},
		{
			name:   "refused with 406",
			header: `{"min_version":"0","max_version":"2","request_version":"-1","response_version":"-1"}`,
			want:   ServerAPIVersion{Min: 0, Max: 2, Request: -1, Response: -1},
			ok:     true,
		},
		{
			name:   "bare numbers",
			header: `{"min_version":1,"max_version":2,"request_version":1,"response_version":1}`,
			want:   ServerAPIVersion{Min: 1, Max: 2, Request: 1, Response: 1},
			ok:     true,
		},
		{name: "no header"},
		{name: "not json", header: `API v2`},
		{name: "missing max", header: `{"min_version":"0"}`},
		{name: "missing min", header: `{"max_version":"2"}`},
		{name: "not a number", header: `{"min_version":"zero","max_version":"2"}`},
		{name: "bad request_version", header: `{"min_version":"0","max_version":"2","request_version":"x"}`},
		{name: "bad response_version", header: `{"min_version":"0","max_version":"2","response_version":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := responseWithVersion(tc.header).ServerAPIVersion()
			if ok != tc.ok || got != tc.want {
				t.Errorf("ServerAPIVersion() = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestResponse_ServerAPIVersion_NilSafe(t *testing.T) {
	var r *Response
	if _, ok := r.ServerAPIVersion(); ok {
		t.Error("nil *Response reported a version")
	}
	if _, ok := (&Response{}).ServerAPIVersion(); ok {
		t.Error("Response without HTTPResponse reported a version")
	}
}

// Any call's Response carries the header, including a failed one.
func TestResponse_ServerAPIVersion_FromCall(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/missing", cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
	handler := srv.Server.Config.Handler
	srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ops-Server-API-Version", `{"min_version":"0","max_version":"2","request_version":"1","response_version":"1"}`)
		handler.ServeHTTP(w, r)
	})
	c := newTestClient(t, srv.Server)
	_, resp, err := c.Nodes.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get: err = %v, want ErrNotFound", err)
	}
	if v, ok := resp.ServerAPIVersion(); !ok || v.Max != 2 {
		t.Errorf("ServerAPIVersion() = %+v, %v", v, ok)
	}
}

func TestClient_ServerAPIVersion(t *testing.T) {
	t.Run("erchef body with header", func(t *testing.T) {
		srv := cinctest.New(t)
		// erchef's body carries only the range, as integers; the header has
		// the negotiated versions.
		srv.Handle("GET /server_api_version", cinctest.Route{Body: `{"min_api_version":0,"max_api_version":2}`})
		handler := srv.Server.Config.Handler
		srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Ops-Server-API-Version", `{"min_version":"0","max_version":"2","request_version":"1","response_version":"1"}`)
			handler.ServeHTTP(w, r)
		})
		c := newTestClient(t, srv.Server)
		v, resp, err := c.ServerAPIVersion(context.Background())
		if err != nil {
			t.Fatalf("ServerAPIVersion: %v", err)
		}
		if resp == nil || *v != (ServerAPIVersion{Min: 0, Max: 2, Request: 1, Response: 1}) {
			t.Errorf("ServerAPIVersion = %+v", v)
		}
	})

	t.Run("body alone", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /server_api_version", cinctest.Route{
			Body: `{"min_api_version":1,"max_api_version":3,"request_version":1,"response_version":1}`,
		})
		c := newTestClient(t, srv.Server)
		v, _, err := c.ServerAPIVersion(context.Background())
		if err != nil {
			t.Fatalf("ServerAPIVersion: %v", err)
		}
		if *v != (ServerAPIVersion{Min: 1, Max: 3, Request: 1, Response: 1}) {
			t.Errorf("ServerAPIVersion = %+v", v)
		}
	})

	t.Run("erchef body without header", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /server_api_version", cinctest.Route{Body: `{"min_api_version":0,"max_api_version":2}`})
		c := newTestClient(t, srv.Server)
		v, _, err := c.ServerAPIVersion(context.Background())
		if err != nil {
			t.Fatalf("ServerAPIVersion: %v", err)
		}
		if *v != (ServerAPIVersion{Min: 0, Max: 2}) {
			t.Errorf("ServerAPIVersion = %+v", v)
		}
	})

	// A 200 that reports no range (a proxy's page, a server that is not a Chef
	// Server) is not version 0.
	for _, body := range []string{`{}`, `{"min_api_version":0}`, `{"max_api_version":2}`, `{"min_api_version":null,"max_api_version":2}`} {
		t.Run("no range in "+body, func(t *testing.T) {
			srv := cinctest.New(t)
			srv.Handle("GET /server_api_version", cinctest.Route{Body: body})
			c := newTestClient(t, srv.Server)
			v, resp, err := c.ServerAPIVersion(context.Background())
			if !errors.Is(err, ErrNoServerAPIVersion) || v != nil || resp == nil {
				t.Errorf("ServerAPIVersion = %+v, %v, %v; want nil, a response and ErrNoServerAPIVersion", v, resp, err)
			}
		})
	}

	t.Run("range from the header when the body has none", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /server_api_version", cinctest.Route{Body: `{}`})
		handler := srv.Server.Config.Handler
		srv.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Ops-Server-API-Version", `{"min_version":"0","max_version":"2","request_version":"1","response_version":"1"}`)
			handler.ServeHTTP(w, r)
		})
		c := newTestClient(t, srv.Server)
		v, _, err := c.ServerAPIVersion(context.Background())
		if err != nil {
			t.Fatalf("ServerAPIVersion: %v", err)
		}
		if *v != (ServerAPIVersion{Min: 0, Max: 2, Request: 1, Response: 1}) {
			t.Errorf("ServerAPIVersion = %+v", v)
		}
	})

	t.Run("error", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /server_api_version", cinctest.Route{Status: 404, Body: `{"error":["no"]}`})
		c := newTestClient(t, srv.Server)
		v, resp, err := c.ServerAPIVersion(context.Background())
		if !errors.Is(err, ErrNotFound) || v != nil || resp == nil {
			t.Errorf("ServerAPIVersion = %+v, %v, %v; want nil, a response and ErrNotFound", v, resp, err)
		}
	})
}
