package cinc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// TestSandbox_ChecksumNullValues asserts that the POST /sandboxes request body
// encodes checksum values as JSON null, not as {}.
func TestSandbox_ChecksumNullValues(t *testing.T) {
	ck := md5Hex([]byte("hello"))
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/sandboxes", cinctest.Route{
		Status: 201,
		Body:   `{"sandbox_id":"sb1","checksums":{"` + ck + `":{"needs_upload":false,"url":""}}}`,
		Assert: func(t *testing.T, r *http.Request, body []byte) {
			// Decode into a raw map so we can inspect the value type.
			var req struct {
				Checksums map[string]json.RawMessage `json:"checksums"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("unmarshal request body: %v (body=%s)", err, body)
			}
			val, ok := req.Checksums[ck]
			if !ok {
				t.Fatalf("checksum key %q not found in body %s", ck, body)
			}
			if strings.TrimSpace(string(val)) != "null" {
				t.Errorf("checksum value = %s, want null", val)
			}
		},
	})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.createSandbox(context.Background(), []string{ck}); err != nil {
		t.Fatalf("createSandbox: %v", err)
	}
}

func TestSandbox_CreateAndUpload(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/sandboxes",
		cinctest.Route{Status: 201, Body: `{
			"sandbox_id":"sb1",
			"checksums":{
				"` + md5Hex([]byte("hello")) + `":{"needs_upload":true,"url":"` + "PUTURL" + `"}
			}}`})
	c := newTestClient(t, srv.Server)

	sb, _, err := c.createSandbox(context.Background(),
		[]string{md5Hex([]byte("hello"))})
	if err != nil {
		t.Fatalf("createSandbox: %v", err)
	}
	entry := sb.Checksums[md5Hex([]byte("hello"))]
	if !entry.NeedsUpload || entry.URL == "" {
		t.Fatalf("checksum entry: %+v", entry)
	}

	// uploadFile PUTs raw bytes to the signed URL.
	put := cinctest.New(t)
	var gotBody string
	put.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(200)
	})
	if err := c.uploadFile(context.Background(), put.Server.URL, []byte("hello")); err != nil {
		t.Fatalf("uploadFile: %v", err)
	}
	if gotBody != "hello" {
		t.Fatalf("uploaded body = %q", gotBody)
	}
}
