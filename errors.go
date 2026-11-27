package cinc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Sentinel errors for use with errors.Is.
var (
	// ErrNotFound is a 404: the object, or the organization, does not exist.
	ErrNotFound = errors.New("cinc: not found")
	// ErrConflict is a 409: an object of that name already exists, or (for a
	// cookbook version) it is frozen; see ErrCookbookFrozen.
	ErrConflict = errors.New("cinc: conflict")
	// ErrForbidden is a 403: the requestor lacks the permission, or the
	// server refused the change outright (see ErrAdminsRequiredOnGrant).
	ErrForbidden = errors.New("cinc: forbidden")
	// ErrUnauthorized is a 401: the signature did not verify, which is
	// nearly always the wrong key or a skewed clock.
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
	// ErrCookbookFrozen is Cookbooks.Upload refused because the version is
	// frozen; upload WithForce to replace it. It also matches ErrConflict.
	ErrCookbookFrozen = errors.New("cinc: cookbook version is frozen")
)

// ErrorResponse describes a non-2xx response from the Chef server.
type ErrorResponse struct {
	Method     string   // request method
	Path       string   // request path
	StatusCode int      // HTTP status code
	Messages   []string // server-reported error messages

	// Name is the name of the object a create sent, such as "web1" for
	// Nodes.Create of a node named web1. A create POSTs to the collection,
	// so Path does not say which object a 409 conflict is about; Name does.
	// It is "" when the request was not a create of a named object.
	Name string
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

// withName records name as the Name of the *ErrorResponse in err, if err
// holds one, and returns err unchanged otherwise. An empty name sets nothing.
func withName(err error, name string) error {
	var er *ErrorResponse
	if name != "" && errors.As(err, &er) {
		er.Name = name
	}
	return err
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
		er.Messages = []string{truncate(strings.TrimSpace(string(body)), maxRawErrorBody)}
	}
	return er
}

// maxRawErrorBody caps an error body that is not a Chef error document (a
// proxy's HTML page, say), so it cannot bloat an error message or a log.
const maxRawErrorBody = 512

// truncate returns s cut to at most n bytes, on a rune boundary, with an
// ellipsis when anything was cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
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
