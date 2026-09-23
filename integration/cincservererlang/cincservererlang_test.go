// Package cincservererlang runs the shared integration suite against the CINC
// Server Erlang stack (erchef) that integration/terraform brings up in AWS.
// It is run by hand, through integration/run-cinc-server-erlang.sh; without a
// target file the tests skip, so CI compiles and lints this package but never
// contacts AWS.
package cincservererlang

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/cinc-project/cinc-api/integration/suite"
)

// readyTimeout bounds the wait for a freshly booted stack. Package install
// plus two reconfigures took about 5 minutes on the first run; the margin
// covers slow package downloads.
const readyTimeout = 20 * time.Minute

func TestCincServerErlang(t *testing.T) {
	path := targetPath()
	tf, err := loadTarget(path)
	if errors.Is(err, errNoTarget) {
		t.Skipf("%v; run integration/run-cinc-server-erlang.sh", err)
	}
	if err != nil {
		t.Fatalf("load target: %v", err)
	}

	caPEM, err := os.ReadFile(tf.CACertPath)
	if err != nil {
		t.Fatalf("read CA certificate: %v", err)
	}
	hc, err := httpClientTrusting(caPEM)
	if err != nil {
		t.Fatalf("CA certificate %s: %v", tf.CACertPath, err)
	}
	keyPEM, err := os.ReadFile(tf.KeyPath)
	if err != nil {
		t.Fatalf("read admin key: %v", err)
	}
	key, err := cinc.ParseKey(keyPEM)
	if err != nil {
		t.Fatalf("parse admin key %s: %v", tf.KeyPath, err)
	}
	cfg := cinc.Config{ServerURL: tf.ServerURL, Org: tf.Org, ClientName: tf.Admin, Key: key}
	newClient := func(t *testing.T) *cinc.Client {
		t.Helper()
		c, err := cinc.NewClient(cfg, cinc.WithHTTPClient(hc))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		return c
	}

	// The stack is ready once the server answers and the bootstrap has
	// created the admin and the org.
	c := newClient(t)
	if err := waitReady(t.Context(), readyTimeout, 15*time.Second, func(ctx context.Context) error {
		if _, _, err := c.Status.Get(ctx); err != nil {
			return err
		}
		_, _, err := c.Orgs.Get(ctx, tf.Org)
		return err
	}); err != nil {
		t.Fatalf("%s: %v\nDebug the bootstrap with: aws ssm start-session --target <instance_id>, then read /var/log/cloud-init-output.log", tf.ServerURL, err)
	}

	suite.Run(t, suite.Target{
		Name:          "cinc-server-erlang",
		Client:        newClient,
		Org:           tf.Org,
		Admin:         tf.Admin,
		ServerURL:     tf.ServerURL,
		Key:           key,
		HTTPClient:    hc,
		StatsUser:     tf.StatsUser,
		StatsPassword: tf.StatsPassword,
		// The admin is a server-admin, not the pivotal superuser, whose key
		// never leaves the instance. erchef reserves these to pivotal: each
		// answers the server-admin with 403 "missing create permission"
		// (observed on CINC Server 15.10.125).
		Gaps: map[string]string{
			"orgs/lifecycle":          "POST /organizations needs the pivotal superuser; a server-admin gets 403",
			"users/authenticate":      "POST /authenticate_user needs the pivotal superuser; a server-admin gets 403",
			"associations/add-member": "POST /organizations/O/users (association without an invitation) needs the pivotal superuser; a server-admin gets 403",
		},
	})
}
