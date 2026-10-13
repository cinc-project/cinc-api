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
	// No password is configured, so the server generated one for /_stats.
	statsUser, statsPassword := srv.StatsCredentials()

	// Make the admin a member of the org, as the cinc-server-erlang stack's
	// admin is. The bootstrap superuser is not one by default, on
	// cinc-server-ng as on Chef Infra Server.
	admin, err := cinc.NewClient(cfg, cinc.WithHTTPClient(hc))
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewClient: %v\n", err)
		return 1
	}
	if _, err := admin.Associations.AddMember(context.Background(), srv.AdminName()); err != nil {
		fmt.Fprintf(os.Stderr, "associate %s with %s: %v\n", srv.AdminName(), org, err)
		return 1
	}

	target = suite.Target{
		Name:          "cinc-server-ng",
		Org:           org,
		Admin:         srv.AdminName(),
		ServerURL:     srv.URL(),
		Key:           key,
		HTTPClient:    hc,
		StatsUser:     statsUser,
		StatsPassword: statsPassword,
		Gaps: map[string]string{
			"keys/rename": "key PUT ignores a new name and drops omitted fields: https://github.com/cinc-project/cinc-server-ng/issues/163",
			"stats/get":   "/_stats JSON shape differs from erchef (numeric values, labels array): https://github.com/cinc-project/cinc-server-ng/issues/243",
			// Not a server difference: erchef also lets the superuser take
			// admins off a grant ACE, and this target's admin is the superuser.
			"acls/revoke-keeps-admins-on-grant": "the admin here is the pivotal superuser, which erchef also exempts from this check",
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
