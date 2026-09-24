// Package cincserverng runs the shared integration suite against an in-memory
// cinc-server-ng on every CI job. Auth is on, so the server itself verifies
// every request's Mixlib v1.3 signature — unlike the unit tests' cinctest fake,
// this exercises the full signed-request path end to end.
package cincserverng

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/cinc-project/cinc-api/integration/suite"
	"github.com/cinc-project/cinc-server-ng/server"
)

const org = "test"

// target is set by TestMain once the server is up.
var target suite.Target

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run starts one server for the whole package: the suite gives every object a
// unique name, so the tests share it safely and in parallel.
func run(m *testing.M) int {
	srv, err := server.New(server.Options{Orgs: []string{org}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "server.New: %v\n", err)
		return 1
	}
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "server.Start: %v\n", err)
		return 1
	}

	// Keep the http.Client so its idle keep-alive connections can be closed
	// before stopping the server. Otherwise graceful shutdown blocks until they
	// time out, which takes seconds after a parallel cookbook download.
	hc := &http.Client{Timeout: 30 * time.Second}
	defer func() {
		hc.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	key, err := cinc.ParseKey(srv.AdminKey())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ParseKey admin key: %v\n", err)
		return 1
	}
	cfg := cinc.Config{ServerURL: srv.URL(), Org: org, ClientName: srv.AdminName(), Key: key}

	target = suite.Target{
		Name:       "cinc-server-ng",
		Org:        org,
		Admin:      srv.AdminName(),
		ServerURL:  srv.URL(),
		Key:        key,
		HTTPClient: hc,
		// Placeholders: cinc-server-ng wants a signed /_stats request, not
		// Basic auth, so stats/get is a gap below.
		StatsUser:     "statsuser",
		StatsPassword: "unused",
		Gaps: map[string]string{
			"principals/get": "returns the API v0 single-object shape under v1: https://github.com/cinc-project/cinc-server-ng/issues/162",
			"keys/rename":    "key PUT ignores a new name and drops omitted fields: https://github.com/cinc-project/cinc-server-ng/issues/163",
			"stats/get":      "requires a signed request instead of HTTP Basic auth: https://github.com/cinc-project/cinc-server-ng/issues/164",
			"cookbooks/rejects-manifest-without-metadata": "accepts a manifest without metadata: https://github.com/cinc-project/cinc-server-ng/issues/160",
			"cookbooks/rejects-all-files-under-api-v1":    "accepts all_files under API v1: https://github.com/cinc-project/cinc-server-ng/issues/160",
			"clients/rejects-key-field-on-update":         "accepts and stores create_key on a client PUT: https://github.com/cinc-project/cinc-server-ng/issues/165",
			"acls/revoke-keeps-admins-on-grant":           "lets a non-superuser remove admins from a grant ACE: https://github.com/cinc-project/cinc-server-ng/issues/211",
			"cookbooks/list-defaults-to-one-version":      "lists every version on GET /cookbooks without num_versions: https://github.com/cinc-project/cinc-server-ng/issues/212",
			"cookbooks/rejects-invalid-num-versions":      "accepts an invalid num_versions: https://github.com/cinc-project/cinc-server-ng/issues/212",
		},
		Client: func(t *testing.T) *cinc.Client {
			t.Helper()
			c, err := cinc.NewClient(cfg, cinc.WithHTTPClient(hc))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			return c
		},
	}
	return m.Run()
}

func TestCincServerNG(t *testing.T) {
	suite.Run(t, target)
}
