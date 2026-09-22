// Package suite holds the integration tests shared by every server target.
// Each target package (cincserverng for CI, and later cincservererlang for the
// CINC Server Erlang stack) builds a Target and calls Run, so a test written
// here runs unchanged against both servers. A difference between them shows up
// as a named entry in Target.Gaps, never as a test that exists for one only.
package suite

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// Target is one server the suite runs against.
type Target struct {
	// Name identifies the server in skip messages, e.g. "cinc-server-ng".
	Name string
	// Client returns a client authenticated as an admin of Org.
	Client func(t *testing.T) *cinc.Client
	// Org is the organization the tests create objects in.
	Org string
	// Gaps maps a case name (e.g. "nodes/lifecycle") to the reason it is
	// skipped on this target: an upstream issue URL, or the behaviour
	// observed. Every key must name an existing case and carry a reason.
	Gaps map[string]string
}

// testCase is one shared test. name is "<family>/<case>" and becomes the
// subtest name, so `go test -run 'Test.*/nodes/'` selects a family.
type testCase struct {
	name string
	run  func(t *testing.T, c *cinc.Client)
}

var cases = []testCase{
	{"status/get", testStatusGet},
	{"nodes/lifecycle", testNodeLifecycle},
	{"nodes/not-found", testNodeNotFound},
	{"search/query", testSearchQuery},
	{"clients/lifecycle", testClientLifecycle},
	{"cookbooks/upload-download", testCookbookUploadDownload},
	{"cookbook-artifacts/upload", testCookbookArtifactUpload},
	{"policies/push-revision-two-groups", testPushRevisionToTwoGroups},
	{"roles/lifecycle", testRoleLifecycle},
	{"environments/lifecycle", testEnvironmentLifecycle},
	{"environments/default-read-only", testEnvironmentDefaultReadOnly},
	{"data-bags/lifecycle", testDataBagLifecycle},
	{"data-bags/encrypted-round-trip", testDataBagEncryptedRoundTrip},
	{"data-bags/chef-encrypted-formats", testDataBagChefEncryptedFormats},
}

// Run runs every shared case against tgt as a parallel subtest, skipping the
// ones tgt.Gaps lists.
func Run(t *testing.T, tgt Target) {
	t.Helper()
	names := make([]string, len(cases))
	for i, tc := range cases {
		names[i] = tc.name
	}
	if err := checkGaps(names, tgt.Gaps); err != nil {
		t.Fatalf("%s: %v", tgt.Name, err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if reason, ok := tgt.Gaps[tc.name]; ok {
				t.Skipf("known gap on %s: %s", tgt.Name, reason)
			}
			t.Parallel()
			tc.run(t, tgt.Client(t))
		})
	}
}

// checkGaps rejects a gap that names no case (a typo or a renamed test would
// otherwise skip nothing and hide the mistake) or that gives no reason.
func checkGaps(names []string, gaps map[string]string) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(gaps)) {
		if !slices.Contains(names, name) {
			errs = append(errs, fmt.Errorf("gap %q names no test", name))
		}
		if strings.TrimSpace(gaps[name]) == "" {
			errs = append(errs, fmt.Errorf("gap %q has no reason", name))
		}
	}
	return errors.Join(errs...)
}
