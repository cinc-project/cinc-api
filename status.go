package cinc

import (
	"context"
	"encoding/json"
	"fmt"
)

// ServerStatus is the response from GET /_status. The endpoint is typically
// unauthenticated and is used by load balancers as a health probe.
type ServerStatus struct {
	// Status is "pong" when every upstream answered, "fail" otherwise.
	Status string `json:"status"`
	// ServerVersion is set only when the server is configured to report it
	// (erchef's include_version_in_status).
	ServerVersion string `json:"server_version,omitempty"`
	// Upstreams maps each service the server pinged to "pong" or "fail".
	Upstreams map[string]string `json:"upstreams,omitempty"`
	Keygen    KeygenStatus      `json:"keygen"`
	// Indexing is the search indexer's status, such as {"mode": "batch"}.
	Indexing map[string]any `json:"indexing,omitempty"`
}

// KeygenStatus reports on the server's pre-generated keypair pool.
type KeygenStatus struct {
	Keys          int `json:"keys"`
	Max           int `json:"max"`
	MaxWorkers    int `json:"max_workers"`
	CurMaxWorkers int `json:"cur_max_workers"`
	Inflight      int `json:"inflight"`
	AvailWorkers  int `json:"avail_workers"`
	StartSize     int `json:"start_size"`
}

// StatusService accesses the top-level /_status endpoint.
type StatusService struct{ client *Client }

// Get returns the server's health and keygen pool status.
//
// When an upstream is down the server answers 500 with the same body and a
// Status of "fail". Get then returns that status alongside the
// *ErrorResponse, so the caller can see which upstream failed. It makes a
// single attempt: a health probe reports the server's state now rather than
// retrying until it improves.
func (s *StatusService) Get(ctx context.Context) (*ServerStatus, *Response, error) {
	data, resp, err := s.client.doOnce(ctx, "GET", "/_status", nil)
	var st ServerStatus
	if err == nil {
		if len(data) > 0 {
			if err := json.Unmarshal(data, &st); err != nil {
				return nil, resp, fmt.Errorf("cinc: decode response: %w", err)
			}
		}
		return &st, resp, nil
	}
	// A failing server's body is still a status; a proxy's error page is not.
	if resp != nil && json.Unmarshal(data, &st) == nil && st.Status != "" {
		return &st, resp, err
	}
	return nil, resp, err
}
