package cinc

import (
	"context"
	"errors"
)

// PushJobRequest is the body sent to create a push job. The server requires
// Command and Nodes; Quorum controls how many nodes must acknowledge before
// the job can complete, and RunTimeout bounds the whole run in seconds.
type PushJobRequest struct {
	Command       string            `json:"command"`
	Quorum        int               `json:"quorum"`
	RunTimeout    int               `json:"run_timeout,omitempty"`
	Nodes         []string          `json:"nodes"`
	Env           map[string]string `json:"env,omitempty"`
	CaptureOutput bool              `json:"capture_output,omitempty"`
}

// PushJobRef identifies a created job. Current Chef/CINC servers return the
// job's id; older pushy servers return its URI instead, so both are decoded.
type PushJobRef struct {
	ID  string `json:"id,omitempty"`
	URI string `json:"uri,omitempty"`
}

// NodeStates groups a job's target nodes by their outcome. The server reports
// each state in its own list: new, ready, running, complete, crashed, aborted,
// nacked and unavailable (docs.chef.io, Push Jobs API).
type NodeStates struct {
	New         []string `json:"new,omitempty"`
	Ready       []string `json:"ready,omitempty"`
	Running     []string `json:"running,omitempty"`
	Complete    []string `json:"complete,omitempty"`
	Crashed     []string `json:"crashed,omitempty"`
	Aborted     []string `json:"aborted,omitempty"`
	Nacked      []string `json:"nacked,omitempty"`
	Unavailable []string `json:"unavailable,omitempty"`
}

// PushJobStatus is the full state of a job as returned by Get.
type PushJobStatus struct {
	ID            string            `json:"id,omitempty"`
	Command       string            `json:"command,omitempty"`
	RunTimeout    int               `json:"run_timeout,omitempty"`
	Status        string            `json:"status,omitempty"`
	CreatedAt     string            `json:"created_at,omitempty"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
	User          string            `json:"user,omitempty"`
	Dir           string            `json:"dir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	CaptureOutput bool              `json:"capture_output,omitempty"`
	Nodes         NodeStates        `json:"nodes,omitzero"`
}

// PushJobsService accesses the /pushy/jobs endpoints of the push-jobs
// (pushy) subsystem.
type PushJobsService struct{ client *Client }

// Create starts a push job running Command on the given Nodes. The returned
// ref carries the job's id (and, on older servers, its URI); feed the id to
// Get to follow the job to completion.
func (s *PushJobsService) Create(ctx context.Context, req *PushJobRequest) (*PushJobRef, *Response, error) {
	if req == nil {
		return nil, nil, errors.New("cinc: push job request must not be nil")
	}
	body := *req
	body.Nodes = nonNil(body.Nodes)
	ref, resp, err := do[PushJobRef](ctx, s.client, "POST",
		s.client.orgPath("/pushy/jobs"), body)
	return ptrOrNil(ref, err), resp, err
}

// Get returns the status of a job by id, including the per-node outcomes.
func (s *PushJobsService) Get(ctx context.Context, id string) (*PushJobStatus, *Response, error) {
	st, resp, err := do[PushJobStatus](ctx, s.client, "GET",
		s.client.orgPath("/pushy/jobs/"+id), nil)
	return ptrOrNil(st, err), resp, err
}
