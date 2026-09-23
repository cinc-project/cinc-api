package cinc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A signed request must not follow a redirect: Go forwards the X-Ops-*
// signature headers to whatever host Location names, handing it a request it
// can replay for the server's clock-skew window (#86). The 3xx surfaces as an
// error naming the Location instead, for the default client and for one
// passed with WithHTTPClient, with or without WithSkipTLSVerify.
func TestSignedRequest_DoesNotFollowRedirects(t *testing.T) {
	cases := []struct {
		name string
		opts func() []Option
	}{
		{"default_client", func() []Option { return nil }},
		{"with_http_client", func() []Option { return []Option{WithHTTPClient(&http.Client{Timeout: 5 * time.Second})} }},
		{"with_http_client_and_skip_verify", func() []Option {
			return []Option{WithHTTPClient(&http.Client{}), WithSkipTLSVerify(true)}
		}},
	}
	for _, tc := range cases {
		for _, method := range []string{"GET", "POST"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				var leaked atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					leaked.Add(1)
					if r.Header.Get("X-Ops-Authorization-1") != "" {
						t.Error("the redirect target received the request signature")
					}
				}))
				defer target.Close()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, target.URL+"/elsewhere", http.StatusTemporaryRedirect)
				}))
				defer srv.Close()
				c, err := NewClient(Config{ServerURL: srv.URL, Org: "o", ClientName: "c", Key: testRSAKey(t)}, tc.opts()...)
				if err != nil {
					t.Fatal(err)
				}
				sleeps := recordSleeps(c)

				_, _, err = do[map[string]any](context.Background(), c, method, "/nodes/x", map[string]any{})
				if err == nil {
					t.Fatal("want an error for the redirect")
				}
				var er *ErrorResponse
				if !errors.As(err, &er) || er.StatusCode != http.StatusTemporaryRedirect {
					t.Fatalf("err = %v, want an *ErrorResponse with status 307", err)
				}
				if !strings.Contains(err.Error(), target.URL+"/elsewhere") {
					t.Errorf("err = %q, want it to name the Location", err)
				}
				if n := leaked.Load(); n != 0 {
					t.Errorf("the redirect was followed %d times", n)
				}
				if len(*sleeps) != 0 {
					t.Errorf("a redirect was retried %d times", len(*sleeps))
				}
			})
		}
	}
}

// A relative Location is reported resolved against the request URL, an
// unparseable one as sent, and a 3xx with no Location still surfaces as an
// error. (net/http itself rejects an unparseable Location on the statuses it
// would follow, so that case uses 300 Multiple Choices, which it never does.)
func TestSignedRequest_RedirectLocationReporting(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
		want     string
		onServer bool // want is relative to the server URL
	}{
		{"relative", http.StatusFound, "/elsewhere", "/elsewhere", true},
		{"unparseable", http.StatusMultipleChoices, "%zz", "redirected to %zz", false},
		{"missing", http.StatusFound, "", "no Location", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			_, _, err := do[map[string]any](context.Background(), c, "GET", "/nodes/x", nil)
			if err == nil {
				t.Fatal("want an error for the redirect")
			}
			want := tc.want
			if tc.onServer {
				want = srv.URL + tc.want
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to contain %q", err, want)
			}
		})
	}
}

// Refusing redirects must not reach into the caller's *http.Client, and the
// unsigned bookshelf transfers keep the caller's redirect policy.
func TestWithHTTPClient_RedirectPolicyCopiesCallerClient(t *testing.T) {
	errCallers := errors.New("caller's policy")
	callers := func(*http.Request, []*http.Request) error { return errCallers }
	for _, skip := range []bool{false, true} {
		custom := &http.Client{CheckRedirect: callers}
		c, err := NewClient(Config{ServerURL: "https://h", Org: "o", ClientName: "c", Key: testRSAKey(t)},
			WithHTTPClient(custom), WithSkipTLSVerify(skip))
		if err != nil {
			t.Fatal(err)
		}
		if got := custom.CheckRedirect(nil, nil); !errors.Is(got, errCallers) {
			t.Errorf("skip=%v: the caller's CheckRedirect was replaced", skip)
		}
		if got := c.httpClient.CheckRedirect(nil, nil); !errors.Is(got, http.ErrUseLastResponse) {
			t.Errorf("skip=%v: signed client CheckRedirect returned %v, want ErrUseLastResponse", skip, got)
		}
		if c.transferClient.CheckRedirect == nil || !errors.Is(c.transferClient.CheckRedirect(nil, nil), errCallers) {
			t.Errorf("skip=%v: transfers lost the caller's CheckRedirect", skip)
		}
	}
}

// The default client's transfers follow redirects (S3 redirects across
// regions); only signed requests refuse them.
func TestDefaultClient_TransfersFollowRedirects(t *testing.T) {
	c, err := NewClient(Config{ServerURL: "https://h", Org: "o", ClientName: "c", Key: testRSAKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if c.transferClient.CheckRedirect != nil {
		t.Error("transfers must keep Go's default redirect policy")
	}
}
