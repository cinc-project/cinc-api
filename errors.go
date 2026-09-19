package cinc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for use with errors.Is.
var (
	ErrNotFound     = errors.New("cinc: not found")
	ErrConflict     = errors.New("cinc: conflict")
	ErrForbidden    = errors.New("cinc: forbidden")
	ErrUnauthorized = errors.New("cinc: unauthorized")
)

// ErrorResponse describes a non-2xx response from the Chef server.
type ErrorResponse struct {
	Method     string   // request method
	Path       string   // request path
	StatusCode int      // HTTP status code
	Messages   []string // server-reported error messages
}

func (e *ErrorResponse) Error() string {
	msg := strings.Join(e.Messages, "; ")
	if msg == "" {
		msg = "(no message)"
	}
	// A 401 is nearly always a bad key or a skewed clock, and the server's own
	// message rarely says so. The hint belongs here rather than in Messages,
	// which reports what the server said.
	if e.StatusCode == 401 {
		msg += " (check the client key and that the local clock is in sync)"
	}
	return fmt.Sprintf("cinc: %s %s: %d: %s", e.Method, e.Path, e.StatusCode, msg)
}

// Unwrap maps the status code to a sentinel error for errors.Is.
func (e *ErrorResponse) Unwrap() error {
	switch e.StatusCode {
	case 401:
		return ErrUnauthorized
	case 403:
		return ErrForbidden
	case 404:
		return ErrNotFound
	case 409:
		return ErrConflict
	default:
		return nil
	}
}

// newErrorResponse builds an *ErrorResponse, decoding the Chef error body.
func newErrorResponse(method, path string, code int, body []byte) *ErrorResponse {
	er := &ErrorResponse{Method: method, Path: path, StatusCode: code}
	var parsed struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != nil {
		// "error" may be a []string or a bare string.
		var list []string
		if json.Unmarshal(parsed.Error, &list) == nil {
			er.Messages = list
		} else {
			var s string
			if json.Unmarshal(parsed.Error, &s) == nil {
				er.Messages = []string{s}
			}
		}
	}
	if len(er.Messages) == 0 && len(body) > 0 {
		er.Messages = []string{strings.TrimSpace(string(body))}
	}
	return er
}
