package cincservererlang

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// targetFile is what integration/terraform writes to .out/target.json: the
// only interface between the stack and these tests.
type targetFile struct {
	ServerURL     string `json:"server_url"`
	Org           string `json:"org"`
	Admin         string `json:"admin"`
	KeyPath       string `json:"key_path"`
	CACertPath    string `json:"ca_cert_path"`
	StatsUser     string `json:"stats_user"`
	StatsPassword string `json:"stats_password"`
}

// defaultTargetPath is where Terraform writes the target file, relative to
// this package's directory (go test runs in it).
const defaultTargetPath = "../terraform/.out/target.json"

// targetPath returns CINC_SERVER_ERLANG_TARGET if set, else the default.
func targetPath() string {
	if p := os.Getenv("CINC_SERVER_ERLANG_TARGET"); p != "" {
		return p
	}
	return defaultTargetPath
}

// errNoTarget means no stack is configured; the tests skip rather than fail.
var errNoTarget = errors.New("no cinc-server-erlang target configured")

// loadTarget reads and checks the target file at path. A missing file is
// errNoTarget; anything else wrong with it is a real error.
func loadTarget(path string) (targetFile, error) {
	var tf targetFile
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return tf, fmt.Errorf("%w: %s does not exist", errNoTarget, path)
	}
	if err != nil {
		return tf, err
	}
	if err := json.Unmarshal(data, &tf); err != nil {
		return tf, fmt.Errorf("parse %s: %w", path, err)
	}
	for field, v := range map[string]string{
		"server_url": tf.ServerURL, "org": tf.Org, "admin": tf.Admin,
		"key_path": tf.KeyPath, "ca_cert_path": tf.CACertPath,
		"stats_user": tf.StatsUser, "stats_password": tf.StatsPassword,
	} {
		if v == "" {
			return tf, fmt.Errorf("%s: %s is empty", path, field)
		}
	}
	// Relative paths in the file are relative to the file itself.
	for _, p := range []*string{&tf.KeyPath, &tf.CACertPath} {
		if !filepath.IsAbs(*p) {
			*p = filepath.Join(filepath.Dir(path), *p)
		}
	}
	return tf, nil
}

// httpClientTrusting returns an HTTP client that trusts only the CA
// certificate in caPEM: TLS is verified against the stack's own test CA, never
// skipped.
func httpClientTrusting(caPEM []byte) (*http.Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("no certificate found in the CA file")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: tr, Timeout: 2 * time.Minute}, nil
}

// waitReady calls check every interval until it succeeds, returning nil, or
// until timeout passes, returning check's last error.
func waitReady(ctx context.Context, timeout, interval time.Duration, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		err := check(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not ready after %s: %w", timeout, err)
		case <-time.After(interval):
		}
	}
}
