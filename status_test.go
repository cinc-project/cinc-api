package cinc

import (
	"context"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestStatus_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /_status", cinctest.Route{
		Body: `{
			"status":"pong",
			"upstreams":{"chef_elasticsearch":"pong","oc_bifrost":"pong","postgres":"pong"},
			"keygen":{"keys":10,"max":10,"max_workers":2,"cur_max_workers":2,"inflight":0,"avg_creation_time_in_ms":12.345}
		}`,
	})
	c := newTestClient(t, srv.Server)

	s, _, err := c.Status.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Status != "pong" {
		t.Errorf("Status = %q, want pong", s.Status)
	}
	if s.Upstreams["postgres"] != "pong" {
		t.Errorf("Upstreams[postgres] = %q", s.Upstreams["postgres"])
	}
	if s.Keygen.Keys != 10 || s.Keygen.MaxWorkers != 2 {
		t.Errorf("Keygen = %+v", s.Keygen)
	}
	if s.Keygen.AvgCreationTimeMS == 0 {
		t.Errorf("AvgCreationTimeMS = %v", s.Keygen.AvgCreationTimeMS)
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
