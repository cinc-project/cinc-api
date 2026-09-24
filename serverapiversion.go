package cinc

import (
	"context"
	"encoding/json"
	"errors"
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

// ErrNoServerAPIVersion means GET /server_api_version succeeded but reported
// no API version range, in neither its body nor its X-Ops-Server-API-Version
// header, as from a proxy or a server that is not a Chef Server.
var ErrNoServerAPIVersion = errors.New("cinc: the server did not report which API versions it supports")

// serverAPIVersionBody is the GET /server_api_version body. erchef sends only
// the range; cinc-server-ng adds the negotiated versions. Pointers tell a
// missing field from a zero.
type serverAPIVersionBody struct {
	Min      *int `json:"min_api_version"`
	Max      *int `json:"max_api_version"`
	Request  *int `json:"request_version"`
	Response *int `json:"response_version"`
}

// ServerAPIVersion asks the server which API versions it supports, with a GET
// of the top-level /server_api_version, the cheapest request that reports it
// (erchef accepts it from any authenticated requestor). The range comes from
// the body, or from the response header when the body lacks it; Request and
// Response come from the header, falling back to the body. A response with no
// range in either is ErrNoServerAPIVersion, never a version of 0, matching
// the false Response.ServerAPIVersion reports for it.
//
// Every response carries the same header, so a caller that has just made
// another request can read it from that Response instead.
func (c *Client) ServerAPIVersion(ctx context.Context) (*ServerAPIVersion, *Response, error) {
	body, resp, err := do[serverAPIVersionBody](ctx, c, "GET", "/server_api_version", nil)
	if err != nil {
		return nil, resp, err
	}
	h, fromHeader := resp.ServerAPIVersion()
	var v ServerAPIVersion
	switch {
	case body.Min != nil && body.Max != nil:
		v.Min, v.Max = *body.Min, *body.Max
	case fromHeader:
		v.Min, v.Max = h.Min, h.Max
	default:
		return nil, resp, ErrNoServerAPIVersion
	}
	if fromHeader {
		v.Request, v.Response = h.Request, h.Response
	} else {
		v.Request, v.Response = deref(body.Request), deref(body.Response)
	}
	return &v, resp, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
