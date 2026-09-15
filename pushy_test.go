package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// A job creation is a signed POST to the collection endpoint carrying the
// command, quorum, optional run_timeout and the target node list; the current
// Chef/CINC server answers 201 with the job's id (docs.chef.io, Push Jobs
// API: POST /pushy/jobs → {"id": ...}).
func TestPushJobs_Create(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/pushy/jobs", cinctest.Route{
		Status: 201,
		Body:   `{"id":"aaaaaaaaaaaa25fd67fa8715fd547d3d"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req struct {
				Command    string          `json:"command"`
				Quorum     int             `json:"quorum"`
				RunTimeout int             `json:"run_timeout"`
				Nodes      json.RawMessage `json:"nodes"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode POST body: %v (body=%s)", err, body)
			}
			if req.Command != "chef-client" {
				t.Errorf("command = %q", req.Command)
			}
			if req.Quorum != 1 || req.RunTimeout != 300 {
				t.Errorf("quorum = %d, run_timeout = %d", req.Quorum, req.RunTimeout)
			}
			if string(req.Nodes) != `["node1","node2"]` {
				t.Errorf("nodes = %s, want [\"node1\",\"node2\"]", req.Nodes)
			}
		},
	})

	c := newTestClient(t, srv.Server)
	ref, _, err := c.PushJobs.Create(context.Background(), &PushJobRequest{
		Command:    "chef-client",
		Quorum:     1,
		RunTimeout: 300,
		Nodes:      []string{"node1", "node2"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ref.ID != "aaaaaaaaaaaa25fd67fa8715fd547d3d" {
		t.Errorf("id = %q", ref.ID)
	}
}

// Older pushy servers answer a job creation with the job's URI instead of its
// id; both shapes must decode.
func TestPushJobs_Create_UriResponse(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/pushy/jobs", cinctest.Route{
		Status: 201,
		Body:   `{"uri":"https://h/organizations/o/pushy/jobs/336d0bf2"}`,
	})
	c := newTestClient(t, srv.Server)
	ref, _, err := c.PushJobs.Create(context.Background(), &PushJobRequest{
		Command: "chef-client", Quorum: 1, Nodes: []string{"node1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ref.URI != "https://h/organizations/o/pushy/jobs/336d0bf2" || ref.ID != "" {
		t.Errorf("ref = %+v", ref)
	}
}

// The pushy server validates nodes as a JSON array: a nil Nodes slice must
// serialize as [] rather than null (same rule as run_list and ACL actors).
func TestPushJobs_Create_NilNodesSerializesAsArray(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/pushy/jobs", cinctest.Route{
		Status: 201,
		Body:   `{"id":"x"}`,
		Assert: func(t *testing.T, _ *http.Request, body []byte) {
			var req struct {
				Nodes json.RawMessage `json:"nodes"`
			}
			json.Unmarshal(body, &req)
			if string(req.Nodes) != "[]" {
				t.Errorf("nodes = %s, want []", req.Nodes)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.PushJobs.Create(context.Background(), &PushJobRequest{
		Command: "chef-client", Quorum: 1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// A nil request is a client-side error and must not reach the wire.
func TestPushJobs_Create_RequiresARequest(t *testing.T) {
	srv := cinctest.New(t)
	c := newTestClient(t, srv.Server)
	if _, _, err := c.PushJobs.Create(context.Background(), nil); err == nil {
		t.Fatal("Create with no request returned nil error; want an error and no request")
	}
}

// GET /pushy/jobs/ID returns the full job status, with the per-node outcome
// grouped by state (running/complete/crashed/...).
func TestPushJobs_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/pushy/jobs/aaaaaaaaaaaa25fd67fa8715fd547d3d",
		cinctest.Route{Body: `{
			"id":"aaaaaaaaaaaa25fd67fa8715fd547d3d",
			"command":"chef-client",
			"run_timeout":300,
			"status":"running",
			"created_at":"Tue, 04 Sep 2012 23:01:02 GMT",
			"updated_at":"Tue, 04 Sep 2012 23:17:56 GMT",
			"nodes":{
				"running":["NODE1","NODE5"],
				"complete":["NODE2","NODE3","NODE4"],
				"crashed":["NODE6"]
			},
			"user":"rebecca",
			"dir":"/home/rebecca",
			"env":{"KEY":"value"},
			"capture_output":true
		}`})

	c := newTestClient(t, srv.Server)
	st, _, err := c.PushJobs.Get(context.Background(), "aaaaaaaaaaaa25fd67fa8715fd547d3d")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.ID != "aaaaaaaaaaaa25fd67fa8715fd547d3d" || st.Command != "chef-client" ||
		st.RunTimeout != 300 || st.Status != "running" ||
		st.CreatedAt != "Tue, 04 Sep 2012 23:01:02 GMT" ||
		st.UpdatedAt != "Tue, 04 Sep 2012 23:17:56 GMT" ||
		st.User != "rebecca" || st.Dir != "/home/rebecca" ||
		!st.CaptureOutput {
		t.Errorf("status fields wrong: %+v", st)
	}
	if !reflect.DeepEqual(st.Nodes.Running, []string{"NODE1", "NODE5"}) ||
		!reflect.DeepEqual(st.Nodes.Complete, []string{"NODE2", "NODE3", "NODE4"}) ||
		!reflect.DeepEqual(st.Nodes.Crashed, []string{"NODE6"}) {
		t.Errorf("node states wrong: %+v", st.Nodes)
	}
	if !reflect.DeepEqual(st.Env, map[string]string{"KEY": "value"}) {
		t.Errorf("env wrong: %+v", st.Env)
	}
}

func TestPushJobs_NotFound(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/pushy/jobs/missing",
		cinctest.Route{Status: 404, Body: `{"error":["job not found"]}`})
	c := newTestClient(t, srv.Server)
	_, _, err := c.PushJobs.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected 404")
	}
}
