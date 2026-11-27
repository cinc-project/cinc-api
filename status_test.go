package cinc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// The body erchef's chef_wm_status sends: upstream pings, the keygen cache's
// status_for_json, chef_index:status() and, when include_version_in_status is
// on, the server version.
const erchefStatus = `{
	"server_version":"15.10.125",
	"status":"%s",
	"upstreams":{"chef_solr":"pong","chef_sql":"%s","oc_chef_authz":"pong"},
	"keygen":{"keys":10,"max":10,"max_workers":2,"cur_max_workers":2,"inflight":0,"avail_workers":2,"start_size":0},
	"indexing":{"mode":"batch"}
}`

func TestStatus_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /_status", cinctest.Route{Body: fmt.Sprintf(erchefStatus, "pong", "pong")})
	c := newTestClient(t, srv.Server)

	s, _, err := c.Status.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := &ServerStatus{
		Status:        "pong",
		ServerVersion: "15.10.125",
		Upstreams:     map[string]string{"chef_solr": "pong", "chef_sql": "pong", "oc_chef_authz": "pong"},
		Keygen:        KeygenStatus{Keys: 10, Max: 10, MaxWorkers: 2, CurMaxWorkers: 2, AvailWorkers: 2},
		Indexing:      map[string]any{"mode": "batch"},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("Get = %+v\nwant  %+v", s, want)
	}
}

// When an upstream is down erchef answers 500 with the same body, status
// "fail". The status is what a caller needs to see which upstream failed, so
// it comes back alongside the error, and a health probe is not retried.
func TestStatus_FailureReturnsStatus(t *testing.T) {
	var attempts atomic.Int32
	srv := cinctest.New(t)
	srv.Handle("GET /_status", cinctest.Route{
		Status: http.StatusInternalServerError,
		Body:   fmt.Sprintf(erchefStatus, "fail", "fail"),
		Assert: func(*testing.T, *http.Request, []byte) { attempts.Add(1) },
	})
	c := newTestClient(t, srv.Server)
	recordSleeps(c)

	s, resp, err := c.Status.Get(context.Background())
	if _, ok := errors.AsType[*ErrorResponse](err); !ok {
		t.Fatalf("err = %v, want an *ErrorResponse", err)
	}
	if s == nil || s.Status != "fail" || s.Upstreams["chef_sql"] != "fail" {
		t.Errorf("status = %+v, want the failing status", s)
	}
	if resp == nil || resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("resp = %+v, want the 500", resp)
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

// A 500 whose body is not a status (a proxy's error page) returns no status.
func TestStatus_FailureWithoutStatusBody(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /_status", cinctest.Route{Status: http.StatusBadGateway, Body: `<html>bad gateway</html>`})
	c := newTestClient(t, srv.Server)
	s, _, err := c.Status.Get(context.Background())
	if err == nil || s != nil {
		t.Errorf("Get = %+v, %v; want nil and an error", s, err)
	}
}

func TestStatus_NotOrgScoped(t *testing.T) {
	// /_status must not be prefixed with /organizations/o — if it were, the
	// route below wouldn't match and cinctest would fail the test.
	srv := cinctest.New(t)
	srv.Handle("GET /_status", cinctest.Route{Body: `{"status":"pong"}`})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.Status.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStatus_UndecodableBody(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /_status", cinctest.Route{Body: `not json`})
	c := newTestClient(t, srv.Server)
	if s, _, err := c.Status.Get(context.Background()); err == nil || s != nil {
		t.Errorf("Get = %+v, %v; want nil and a decode error", s, err)
	}
}
