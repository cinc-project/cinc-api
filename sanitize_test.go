package cinc

import (
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestSanitizeServerText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "node web1 not found", "node web1 not found"},
		{"newline and tab kept", "line one\n\tline two", "line one\n\tline two"},
		{"utf-8 kept", "caf\u00e9 \u65e5\u672c", "caf\u00e9 \u65e5\u672c"},
		{"csi clear screen", "a\x1b[2Jb", "ab"},
		{"csi color", "\x1b[31;1mred\x1b[0m", "red"},
		{"csi private mode", "x\x1b[?25ly", "xy"},
		{"csi unterminated", "x\x1b[31", "x"},
		{"csi malformed", "x\x1b[3\x01y", "x y"},
		{"osc bel", "a\x1b]0;pwned\x07b", "ab"},
		{"osc st", "a\x1b]8;;http://evil\x1b\\link\x1b]8;;\x1b\\b", "alinkb"},
		{"osc unterminated", "a\x1b]0;title", "a"},
		{"osc cut by esc", "a\x1b]0;t\x1b[2Jb", "ab"},
		{"dcs", "a\x1bPq#0\x1b\\b", "ab"},
		{"apc", "a\x1b_payload\x1b\\b", "ab"},
		{"two-byte esc", "a\x1bcb", "ab"},
		{"esc with intermediate", "a\x1b(Bb", "ab"},
		{"trailing esc", "a\x1b", "a"},
		{"carriage return", "ok\rFAKE", "ok FAKE"},
		{"other c0", "a\x00b\x08c\x07d", "a b c d"},
		{"del", "a\x7fb", "a b"},
		{"c1 csi", "a\u009b2Jb", "a 2Jb"},
		{"c1 next line", "a\u0085b", "a b"},
		{"bidi override", "evil\u202etxt.exe", "eviltxt.exe"},
		{"bidi embeddings", "\u202aa\u202bb\u202cc\u202dd", "abcd"},
		{"bidi isolates", "\u2066a\u2067b\u2068c\u2069", "abc"},
		{"bidi marks", "a\u200eb\u200fc\u061cd", "abcd"},
		{"invalid utf-8", "a\xffb\xc3", "a\ufffdb\ufffd"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeServerText(tc.in); got != tc.want {
				t.Errorf("sanitizeServerText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Every way newErrorResponse fills Messages from the body is sanitized, so
// both ServerMessage and Error are safe to print.
func TestErrorResponse_SanitizesServerText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"error list", `{"error":["bad\u001b[2J\rthing"]}`, []string{"bad thing"}},
		{"error string", `{"error":"evil\u202etxt"}`, []string{"eviltxt"}},
		{"depsolver object", `{"error":[{"message":"\u001b]0;t\u0007no solution"}]}`, []string{"no solution"}},
		{"top-level message", `{"error":"invalid-x-ops-server-api-version","message":"bad\u0000version"}`,
			[]string{"invalid-x-ops-server-api-version", "bad version"}},
		{"raw body", "proxy\x1b[31m down\r\n", []string{"proxy down"}},
		{"raw body invalid utf-8", "bad \xff byte", []string{"bad \ufffd byte"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			er := newErrorResponse("GET", "/x", 500, []byte(tc.body))
			if !slices.Equal(er.Messages, tc.want) {
				t.Errorf("Messages = %q, want %q", er.Messages, tc.want)
			}
			for _, s := range []string{er.ServerMessage(), er.Error()} {
				if strings.ContainsAny(s, "\x1b\r\x00\u202e\u009b") {
					t.Errorf("output %q still carries control characters", s)
				}
			}
		})
	}
}

// A message that is nothing but escape sequences leaves no empty entry.
func TestErrorResponse_DropsMessagesEmptyAfterSanitizing(t *testing.T) {
	er := newErrorResponse("GET", "/x", 500, []byte(`{"error":["\u001b[2J","real"]}`))
	if !slices.Equal(er.Messages, []string{"real"}) {
		t.Errorf("Messages = %q, want [real]", er.Messages)
	}
}

// The raw body is sanitized before it is capped, so escape sequences do not
// count against maxRawErrorBody.
func TestErrorResponse_RawBodySanitizedBeforeTruncating(t *testing.T) {
	body := strings.Repeat("\x1b[0m", 200) + "tail"
	er := newErrorResponse("GET", "/x", 502, []byte(body))
	if !slices.Equal(er.Messages, []string{"tail"}) {
		t.Errorf("Messages = %q, want [tail]", er.Messages)
	}
}

func TestTransferErrorMessages_Sanitized(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"s3 xml", "<Error><Code>Access\x1b[2JDenied</Code><Message>no\u202eway</Message></Error>", "AccessDenied: noway"},
		{"raw", "gateway\r\x1b]0;x\x07 error", "gateway  error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transferErrorMessages([]byte(tc.body))
			if !slices.Equal(got, []string{tc.want}) {
				t.Errorf("transferErrorMessages = %q, want [%q]", got, tc.want)
			}
		})
	}
}

func TestRedirectError_SanitizesLocation(t *testing.T) {
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "cinc.example", Path: "/nodes/x"}}
	for _, loc := range []string{
		"https://evil\u202e.example/x",  // parses; the host keeps the override
		"https://evil.example/\x1b[2Jx", // does not parse; reported raw
	} {
		r := &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {loc}}, Request: req}
		er := redirectError("GET", "/nodes/x", r)
		msg := er.ServerMessage()
		if strings.ContainsAny(msg, "\x1b\u202e") {
			t.Errorf("Location %q: message %q still carries control characters", loc, msg)
		}
		if !strings.Contains(msg, "evil") {
			t.Errorf("Location %q: message %q no longer names it", loc, msg)
		}
	}
}

// A cookbook manifest checksum is server text too; a mismatch quotes it.
func TestWriteVerified_ChecksumMismatchQuotesServerChecksum(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = writeVerified(root, "f", strings.NewReader("body"), "abc\x1b[2J\u202e")
	if err == nil {
		t.Fatal("want a checksum mismatch")
	}
	if strings.ContainsAny(err.Error(), "\x1b\u202e") {
		t.Errorf("err = %q still carries control characters", err)
	}
}
