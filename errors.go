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
	// ErrBadRequest is a 400: the server refused the request as malformed or
	// naming something that does not exist, such as an unknown actor or group
	// in an ACL write.
	ErrBadRequest = errors.New("cinc: bad request")
	// ErrAdminsRequiredOnGrant is erchef refusing to take the admins group
	// off an object's grant permission, which only the superuser may do. The
	// server answers 403, so the error also matches ErrForbidden, but it says
	// nothing about the caller's own grant permission: erchef checks this
	// rule before it checks authorization. ACLs.Revoke reports it.
	ErrAdminsRequiredOnGrant = errors.New("cinc: the admins group cannot be removed from the grant permission")
)

// ErrorResponse describes a non-2xx response from the Chef server.
type ErrorResponse struct {
	Method     string   // request method
	Path       string   // request path
	StatusCode int      // HTTP status code
	Messages   []string // server-reported error messages
}

// ServerMessage returns what the server said, its Messages joined with "; ",
// without Error's "cinc: METHOD PATH: CODE:" prefix or the 401 hint. It is ""
// when the server sent no message (or e is nil), so a caller can fall back to
// its own wording.
func (e *ErrorResponse) ServerMessage() string {
	if e == nil {
		return ""
	}
	return strings.Join(e.Messages, "; ")
}

func (e *ErrorResponse) Error() string {
	msg := e.ServerMessage()
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
	case 400:
		return ErrBadRequest
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
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != nil {
		// "error" is a bare string, or a list whose elements are strings or,
		// for depsolver failures, objects carrying a "message".
		var list []json.RawMessage
		if json.Unmarshal(parsed.Error, &list) == nil {
			for _, item := range list {
				if m := errorText(item); m != "" {
					er.Messages = append(er.Messages, m)
				}
			}
		} else if m := errorText(parsed.Error); m != "" {
			er.Messages = []string{m}
		}
		// An invalid API version answers with a slug in "error" and the
		// explanation in a top-level "message".
		if parsed.Message != "" {
			er.Messages = append(er.Messages, parsed.Message)
		}
	}
	if len(er.Messages) == 0 && len(body) > 0 {
		er.Messages = []string{strings.TrimSpace(string(body))}
	}
	return er
}

// errorText returns the text of one element of an erchef "error" value: a
// string, or an object's "message".
func errorText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Message
	}
	return ""
}
