// Package cincserverng runs the shared integration suite against an in-memory
// cinc-server-ng on every CI job. Auth is on, so the server itself verifies
// every request's Mixlib v1.3 signature — unlike the unit tests' cinctest fake,
// this exercises the full signed-request path end to end.
//
// The suite runs twice: over plain HTTP, and over HTTPS with cinc-server-ng
// serving TLS itself from a throwaway CA, as a production server does.
package cincserverng

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

// plainTarget and tlsTarget are set by TestMain once the servers are up;
// tlsPool trusts the TLS server's CA.
var (
	plainTarget, tlsTarget suite.Target
	tlsPool                *x509.CertPool
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run starts one plain-HTTP and one TLS server for the whole package: the
// suite gives every object a unique name, so the tests share each server
// safely and in parallel.
func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "cinc-api-tls-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	ca, err := newTestCA(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test CA: %v\n", err)
		return 1
	}
	tlsPool = ca.pool

	var stops []func()
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()

	plain, stop, err := startTarget("cinc-server-ng", server.Options{Orgs: []string{org}}, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stops = append(stops, stop)
	plainTarget = plain

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: ca.pool, MinVersion: tls.VersionTLS12}
	secure, stop, err := startTarget("cinc-server-ng (TLS)", server.Options{
		Orgs:        []string{org},
		TLSCertFile: ca.certFile,
		TLSKeyFile:  ca.keyFile,
	}, &http.Client{Transport: tr, Timeout: 30 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stops = append(stops, stop)
	tlsTarget = secure

	return m.Run()
}

// startTarget starts a server with opts and returns the suite target for it,
// reached through hc, and a func that stops it.
func startTarget(name string, opts server.Options, hc *http.Client) (suite.Target, func(), error) {
	srv, err := server.New(opts)
	if err != nil {
		return suite.Target{}, nil, fmt.Errorf("%s: server.New: %w", name, err)
	}
	if err := srv.Start(); err != nil {
		return suite.Target{}, nil, fmt.Errorf("%s: server.Start: %w", name, err)
	}
	// Close hc's idle keep-alive connections before stopping the server.
	// Otherwise graceful shutdown blocks until they time out, which takes
	// seconds after a parallel cookbook download.
	stop := func() {
		hc.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}

	key, err := cinc.ParseKey(srv.AdminKey())
	if err != nil {
		stop()
		return suite.Target{}, nil, fmt.Errorf("%s: ParseKey admin key: %w", name, err)
	}
	cfg := cinc.Config{ServerURL: srv.URL(), Org: org, ClientName: srv.AdminName(), Key: key}
	// No password is configured, so the server generated one for /_stats.
	statsUser, statsPassword := srv.StatsCredentials()

	// Make the admin a member of the org, as the cinc-server-erlang stack's
	// admin is. The bootstrap superuser is not one by default, on
	// cinc-server-ng as on Chef Infra Server.
	admin, err := cinc.NewClient(cfg, cinc.WithHTTPClient(hc))
	if err != nil {
		stop()
		return suite.Target{}, nil, fmt.Errorf("%s: NewClient: %w", name, err)
	}
	if _, err := admin.Associations.AddMember(context.Background(), srv.AdminName()); err != nil {
		stop()
		return suite.Target{}, nil, fmt.Errorf("%s: associate %s with %s: %w", name, srv.AdminName(), org, err)
	}

	return suite.Target{
		Name:          name,
		Org:           org,
		Admin:         srv.AdminName(),
		ServerURL:     srv.URL(),
		Key:           key,
		HTTPClient:    hc,
		StatsUser:     statsUser,
		StatsPassword: statsPassword,
		Gaps: map[string]string{
			"stats/get": "/_stats JSON shape differs from erchef (numeric values, labels array): https://github.com/cinc-project/cinc-server-ng/issues/243",
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
	}, stop, nil
}

func TestCincServerNG(t *testing.T) {
	suite.Run(t, plainTarget)
}

func TestCincServerNGTLS(t *testing.T) {
	suite.Run(t, tlsTarget)
}
