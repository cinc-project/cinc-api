package cinc

import (
	"context"
	"encoding/json"
	"strconv"
)

// ServerAPIVersion is a Chef Server's API version negotiation: the range of
// X-Ops-Server-API-Version values it accepts and what it made of a request.
type ServerAPIVersion struct {
	// Min and Max bound the API versions the server supports.
	Min, Max int
	// Request is the version the request asked for: 0 when it sent none, -1
	// when the server could not parse it.
	Request int
	// Response is the version the server answered with, -1 when it refused
	// the request as not acceptable (406).
	Response int
}

// ServerAPIVersion parses the X-Ops-Server-API-Version header a Chef Server
// sets on every response, a failed request's included, e.g.
//
//	{"min_version":"0","max_version":"2","request_version":"1","response_version":"1"}
//
// It reports false when the header is missing or has no usable min_version and
// max_version, as from a proxy or a server that is not a Chef Server.
func (r *Response) ServerAPIVersion() (ServerAPIVersion, bool) {
	if r == nil || r.HTTPResponse == nil {
		return ServerAPIVersion{}, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.HTTPResponse.Header.Get("X-Ops-Server-API-Version")), &raw); err != nil {
		return ServerAPIVersion{}, false
	}
	var v ServerAPIVersion
	for _, f := range []struct {
		key      string
		dst      *int
		required bool
	}{
		{"min_version", &v.Min, true},
		{"max_version", &v.Max, true},
		{"request_version", &v.Request, false},
		{"response_version", &v.Response, false},
	} {
		val, present := raw[f.key]
		if !present && !f.required {
			continue
		}
		n, ok := versionNumber(val)
		if !ok {
			return ServerAPIVersion{}, false
		}
		*f.dst = n
	}
	return v, true
}

// versionNumber reads one header value: Chef Servers send the integer as a
// JSON string, but a bare number is accepted too.
func versionNumber(raw json.RawMessage) (int, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// serverAPIVersionBody is the GET /server_api_version body. erchef sends only
// the range; cinc-server-ng adds the negotiated versions.
type serverAPIVersionBody struct {
	Min      int `json:"min_api_version"`
	Max      int `json:"max_api_version"`
	Request  int `json:"request_version"`
	Response int `json:"response_version"`
}

// ServerAPIVersion asks the server which API versions it supports, with a GET
// of the top-level /server_api_version, the cheapest request that reports it
// (erchef accepts it from any authenticated requestor). The range comes from
// the body; Request and Response come from the response header, falling back
// to the body.
//
// Every response carries the same header, so a caller that has just made
// another request can read it from that Response instead.
func (c *Client) ServerAPIVersion(ctx context.Context) (*ServerAPIVersion, *Response, error) {
	body, resp, err := do[serverAPIVersionBody](ctx, c, "GET", "/server_api_version", nil)
	if err != nil {
		return nil, resp, err
	}
	v := ServerAPIVersion(body)
	if h, ok := resp.ServerAPIVersion(); ok {
		v.Request, v.Response = h.Request, h.Response
	}
	return &v, resp, nil
}
