package cinc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// StatMetric is one measured value within a Stat, optionally carrying
// Prometheus-style labels. A counter, gauge or untyped metric sets Value; a
// histogram sets SampleSum, SampleCount and Buckets instead. The server sends
// every number as a string.
type StatMetric struct {
	Value       string            `json:"value,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	SampleSum   string            `json:"sample_sum,omitempty"`
	SampleCount string            `json:"sample_count,omitempty"`
	Buckets     []StatBucket      `json:"bucket,omitempty"`
}

// StatBucket is one bucket of a histogram StatMetric: how many samples were
// at most UpperBound ("+Inf" for the last bucket).
type StatBucket struct {
	CumulativeCount string `json:"cumulative_count"`
	UpperBound      string `json:"upper_bound"`
}

// Stat is one metric family reported by the /_stats endpoint.
type Stat struct {
	Name    string       `json:"name"`
	Type    string       `json:"type"`
	Help    string       `json:"help"`
	Metrics []StatMetric `json:"metrics"`
}

// StatsService accesses the top-level /_stats endpoint, which reports
// connection-pool, PostgreSQL, and Erlang VM statistics.
//
// Unlike every other endpoint, /_stats authenticates with HTTP Basic auth
// (default user "statsuser") rather than the Chef signing protocol, so its
// requests are NOT Chef-signed.
type StatsService struct{ client *Client }

// Get fetches the server statistics as JSON, authenticating with the given
// Basic-auth credentials (the stats user and its password, available from
// `chef-server-ctl show-service-credentials`).
func (s *StatsService) Get(ctx context.Context, user, password string) ([]Stat, *Response, error) {
	u := s.client.baseURLStr + "/_stats?format=json"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: build stats request: %w", err)
	}
	req.SetBasicAuth(user, password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.client.opts.userAgent)

	httpResp, err := s.client.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: GET /_stats: %w", err)
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: read body: %w", err)
	}
	resp := &Response{HTTPResponse: httpResp, StatusCode: httpResp.StatusCode}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, resp, newErrorResponse("GET", "/_stats", httpResp.StatusCode, data)
	}
	var stats []Stat
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, resp, fmt.Errorf("cinc: decode response: %w", err)
	}
	return stats, resp, nil
}
