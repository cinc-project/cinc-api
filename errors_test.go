// errors_test.go
package cinc

import (
	"errors"
	"net/http"
	"slices"
	"testing"
)

func TestErrorResponse_Is(t *testing.T) {
	cases := map[int]error{
		http.StatusNotFound:     ErrNotFound,
		http.StatusConflict:     ErrConflict,
		http.StatusForbidden:    ErrForbidden,
		http.StatusUnauthorized: ErrUnauthorized,
	}
	for code, sentinel := range cases {
		err := newErrorResponse("GET", "/x", code, []byte(`{"error":["boom"]}`))
		if !errors.Is(err, sentinel) {
			t.Errorf("status %d: errors.Is(%v) = false", code, sentinel)
		}
	}
}

func TestErrorResponse_Message(t *testing.T) {
	err := newErrorResponse("PUT", "/nodes/x", 404, []byte(`{"error":["no node"]}`))
	if got := err.Error(); got == "" ||
		!contains(got, "404") || !contains(got, "no node") {
		t.Fatalf("Error() = %q, want code and message", got)
	}
}

func TestErrorResponse_StringErrorField(t *testing.T) {
	// Chef occasionally returns "error" as a bare string instead of a list.
	err := newErrorResponse("GET", "/x", 400, []byte(`{"error":"single string"}`))
	if got := err.Error(); !contains(got, "single string") {
		t.Errorf("Error() = %q, want message text", got)
	}
}

func TestErrorResponse_BareBody(t *testing.T) {
	// Non-JSON body should be surfaced verbatim as the message.
	err := newErrorResponse("GET", "/x", 502, []byte("upstream down"))
	if got := err.Error(); !contains(got, "upstream down") {
		t.Errorf("Error() = %q, want bare body text", got)
	}
}

func TestErrorResponse_EmptyBody(t *testing.T) {
	err := newErrorResponse("GET", "/x", 500, nil)
	if got := err.Error(); !contains(got, "no message") {
		t.Errorf("Error() = %q, want placeholder for empty body", got)
	}
}

func TestErrorResponse_401HintAppended(t *testing.T) {
	// On 401 we append a hint about checking the client key and clock skew.
	err := newErrorResponse("GET", "/x", 401, []byte(`{"error":["bad sig"]}`))
	if got := err.Error(); !contains(got, "clock") || !contains(got, "client key") {
		t.Errorf("Error() = %q, want 401 hint", got)
	}
}

func TestErrorResponse_UnwrapUnknownStatus(t *testing.T) {
	err := newErrorResponse("GET", "/x", 500, nil)
	if got := err.Unwrap(); got != nil {
		t.Errorf("Unwrap on 500 = %v, want nil", got)
	}
}

func TestErrorResponse_FieldsPopulated(t *testing.T) {
	err := newErrorResponse("DELETE", "/y", 409, []byte(`{"error":["conflict"]}`))
	if err.Method != "DELETE" {
		t.Errorf("Method = %q", err.Method)
	}
	if err.Path != "/y" {
		t.Errorf("Path = %q", err.Path)
	}
	if err.StatusCode != 409 {
		t.Errorf("StatusCode = %d", err.StatusCode)
	}
	if len(err.Messages) != 1 || err.Messages[0] != "conflict" {
		t.Errorf("Messages = %v", err.Messages)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(sub) == 0 || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Messages is documented as the server's error messages. The 401 hint is
// client-side advice, so it belongs in Error() output, not in that slice.
func TestErrorResponse_401MessagesAreServerOnly(t *testing.T) {
	err := newErrorResponse("GET", "/x", 401, []byte(`{"error":["bad sig"]}`))
	want := []string{"bad sig"}
	if !slices.Equal(err.Messages, want) {
		t.Errorf("Messages = %q, want %q", err.Messages, want)
	}
	if got := err.Error(); !contains(got, "clock") || !contains(got, "client key") {
		t.Errorf("Error() = %q, want the 401 hint", got)
	}
}

// A 401 with no server message still reads sensibly.
func TestErrorResponse_401WithNoServerMessage(t *testing.T) {
	err := newErrorResponse("GET", "/x", 401, nil)
	if len(err.Messages) != 0 {
		t.Errorf("Messages = %q, want empty", err.Messages)
	}
	if got := err.Error(); !contains(got, "clock") {
		t.Errorf("Error() = %q, want the 401 hint", got)
	}
}

// Other statuses are unaffected.
func TestErrorResponse_NonAuthHasNoHint(t *testing.T) {
	err := newErrorResponse("GET", "/x", 404, []byte(`{"error":["no such node"]}`))
	if got := err.Error(); contains(got, "clock") {
		t.Errorf("Error() = %q, want no 401 hint", got)
	}
}
