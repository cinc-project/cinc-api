package suite

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cinc-project/cinc-api/internal/signing"
)

// rawRequest sends a request signed as tgt.Admin exactly as given — method,
// path, server API version and body — and returns the status and body. It is
// for the negative cases: requests the client's own API cannot produce, to
// check that the server rejects them. path may carry a query string, which
// is sent but, as the protocol requires, not signed.
func rawRequest(t *testing.T, tgt Target, method, path, apiVersion string, body []byte) (int, []byte) {
	t.Helper()
	signedPath, _, _ := strings.Cut(path, "?")
	hdrs, err := signing.SignHeaders(signing.Request{
		Method:     method,
		Path:       signedPath,
		Body:       body,
		UserID:     tgt.Admin,
		Timestamp:  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		APIVersion: apiVersion,
	}, tgt.Key)
	if err != nil {
		t.Fatalf("sign %s %s: %v", method, path, err)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, tgt.ServerURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	for k, v := range hdrs {
		req.Header[k] = v
	}
	req.Header.Set("X-Chef-Version", "16.0.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := tgt.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return resp.StatusCode, out
}
