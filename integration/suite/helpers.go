package suite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// uniqueName returns "t-<kind>-<8 hex>". Every object a test creates gets one,
// so parallel tests, and reruns against a server that kept a failed run's
// objects, never collide. The characters are valid in every Chef object name.
func uniqueName(t *testing.T, kind string) string {
	t.Helper()
	return "t-" + kind + "-" + randomHex(t, 4)
}

// randomHex returns n random bytes as 2n lowercase hex characters, for names
// and for identifiers such as a cookbook artifact's 40-character identifier.
func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// cleanup registers a delete to run when the test ends. A delete that finds
// the object already gone (the test deleted it itself) is fine; any other
// error fails the test, because a leftover object on a long-lived server is a
// bug worth seeing. It uses a fresh context: t.Context() is already cancelled
// when cleanups run.
func cleanup(t *testing.T, what string, del func(ctx context.Context) error) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := del(ctx); err != nil && !errors.Is(err, cinc.ErrNotFound) {
			t.Errorf("cleanup %s: %v", what, err)
		}
	})
}

// eventually retries check until it returns nil or timeout passes, then fails
// the test with check's last error. Search indexing on cinc-server-erlang is
// asynchronous; on cinc-server-ng the first attempt passes.
func eventually(t *testing.T, timeout time.Duration, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still failing after %s: %v", timeout, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("file %s = %q, want %q", path, got, want)
	}
}

// wantStatus fails the test unless err is a server error response with the
// given HTTP status code.
func wantStatus(t *testing.T, err error, code int) {
	t.Helper()
	var er *cinc.ErrorResponse
	if !errors.As(err, &er) {
		t.Fatalf("err = %v, want an HTTP %d error response", err, code)
	}
	if er.StatusCode != code {
		t.Fatalf("status = %d (%v), want %d", er.StatusCode, err, code)
	}
}
