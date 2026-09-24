// Package suite holds the integration tests shared by every server target.
// Each target package (cincserverng for CI, and later cincservererlang for the
// CINC Server Erlang stack) builds a Target and calls Run, so a test written
// here runs unchanged against both servers. A difference between them shows up
// as a named entry in Target.Gaps, never as a test that exists for one only.
package suite

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"maps"
	"net/http"
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
	// Admin is the user name Client authenticates as; it is a member of Org.
	Admin string
	// ServerURL, Key and HTTPClient are what Client is built from. The
	// negative cases use them to sign requests the client itself would never
	// send (see rawRequest).
	ServerURL  string
	Key        *rsa.PrivateKey
	HTTPClient *http.Client
	// StatsUser and StatsPassword are the HTTP Basic credentials for /_stats.
	StatsUser, StatsPassword string
	// Gaps maps a case name (e.g. "nodes/lifecycle") to the reason it is
	// skipped on this target: an upstream issue URL, or the behaviour
	// observed. Every key must name an existing case and carry a reason.
	Gaps map[string]string
}

// testCase is one shared test. name is "<family>/<case>" and becomes the
// subtest name, so `go test -run 'Test.*/nodes/'` selects a family.
type testCase struct {
	name string
	run  func(t *testing.T, tgt Target, c *cinc.Client)
}

var cases = []testCase{
	{"status/get", testStatusGet},
	{"nodes/lifecycle", testNodeLifecycle},
	{"nodes/not-found", testNodeNotFound},
	{"nodes/modify", testNodeModify},
	{"nodes/run-list-edit-normalized", testNodeRunListEditNormalized},
	{"search/query", testSearchQuery},
	{"clients/lifecycle", testClientLifecycle},
	{"cookbooks/upload-download", testCookbookUploadDownload},
	{"cookbook-artifacts/upload", testCookbookArtifactUpload},
	{"policies/push-revision-two-groups", testPushRevisionToTwoGroups},
	{"roles/lifecycle", testRoleLifecycle},
	{"roles/run-list-edit-normalized", testRoleRunListEditNormalized},
	{"environments/lifecycle", testEnvironmentLifecycle},
	{"environments/default-read-only", testEnvironmentDefaultReadOnly},
	{"data-bags/lifecycle", testDataBagLifecycle},
	{"data-bags/encrypted-round-trip", testDataBagEncryptedRoundTrip},
	{"data-bags/chef-encrypted-formats", testDataBagChefEncryptedFormats},
	{"keys/client-lifecycle", testClientKeyLifecycle},
	{"keys/user-lifecycle", testUserKeyLifecycle},
	{"keys/rename", testKeyRename},
	{"groups/lifecycle", testGroupLifecycle},
	{"groups/member-add-remove", testGroupMemberAddRemove},
	{"groups/member-add-drops-unknown", testGroupMemberAddDropsUnknown},
	{"containers/lifecycle", testContainerLifecycle},
	{"acls/objects", testObjectACLs},
	{"acls/org", testOrgACL},
	{"acls/user", testUserACL},
	{"principals/get", testPrincipals},
	{"cookbooks/versions", testCookbookVersions},
	{"cookbooks/version-listing", testCookbookVersionListing},
	{"cookbook-artifacts/list-delete", testCookbookArtifactListDelete},
	{"policies/revisions", testPolicyRevisions},
	{"acls/cookbook-objects", testCookbookObjectACLs},
	{"acls/grant-revoke", testGrantRevoke},
	{"acls/grant-rejects-unknown-members", testGrantRejectsUnknownMembers},
	{"acls/revoke-keeps-admins-on-grant", testRevokeKeepsAdminsOnGrant},
	{"search/partial", testSearchPartial},
	{"search/all-pages", testSearchAllPages},
	{"search/partial-paths-unwrap", testSearchPartialPathsUnwrap},
	{"search/nodes", testSearchNodes},
	{"search/data-bag-items", testSearchDataBagItems},
	{"required-recipe/get", testRequiredRecipe},
	{"license/get", testLicense},
	{"stats/get", testStats},
	{"cookbooks/rejects-manifest-without-metadata", testRejectsManifestWithoutMetadata},
	{"cookbooks/rejects-all-files-under-api-v1", testRejectsAllFilesUnderAPIv1},
	{"cookbooks/list-defaults-to-one-version", testCookbookListDefaultsToOneVersion},
	{"cookbooks/rejects-invalid-num-versions", testRejectsInvalidNumVersions},
	{"clients/rejects-key-field-on-update", testRejectsKeyFieldOnClientUpdate},
	{"users/lifecycle", testUserLifecycle},
	{"users/authenticate", testUserAuthenticate},
	{"associations/invite-accept", testInviteAccept},
	{"associations/invite-reject", testInviteReject},
	{"associations/invite-rescind", testInviteRescind},
	{"associations/add-member", testAddMember},
	{"orgs/lifecycle", testOrgLifecycle},
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
			tc.run(t, tgt, tgt.Client(t))
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
