package cinc

import (
	"context"
	"crypto/md5" //nolint:gosec // Chef's sandbox protocol identifies files by MD5
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	if err := c.uploadFile(context.Background(), put.Server.URL, tempCookbookFile(t, "hello")); err != nil {
		t.Fatalf("uploadFile: %v", err)
	}
	if gotBody != "hello" {
		t.Fatalf("uploaded body = %q", gotBody)
	}
}

// The file is streamed from disk, but the PUT must still carry a
// Content-Length (S3-backed bookshelf rejects a chunked body) and the same
// Content-MD5 as before, derived from the walk-time checksum.
func TestUploadFile_StreamsWithLengthAndMD5(t *testing.T) {
	for _, content := range []string{"package 'nginx'\n", ""} {
		t.Run(strconv.Quote(content), func(t *testing.T) {
			var gotLen int64
			var gotTE []string
			var gotBody, gotMD5 string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotLen, gotTE = r.ContentLength, r.TransferEncoding
				gotMD5 = r.Header.Get("Content-MD5")
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			if err := c.uploadFile(context.Background(), srv.URL, tempCookbookFile(t, content)); err != nil {
				t.Fatalf("uploadFile: %v", err)
			}
			if gotBody != content || gotLen != int64(len(content)) || len(gotTE) != 0 {
				t.Errorf("body %q, Content-Length %d, Transfer-Encoding %v; want %q with its length, not chunked",
					gotBody, gotLen, gotTE, content)
			}
			sum := md5.Sum([]byte(content)) //nolint:gosec // Chef's sandbox protocol
			if want := base64.StdEncoding.EncodeToString(sum[:]); gotMD5 != want {
				t.Errorf("Content-MD5 = %q, want %q", gotMD5, want)
			}
		})
	}
}

// A bookshelf may redirect the PUT (S3 does, across regions); the client
// re-sends the body by reopening the file, as it did from memory before.
func TestUploadFile_FollowsRedirectWithBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusTemporaryRedirect)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	if err := c.uploadFile(context.Background(), srv.URL+"/old", tempCookbookFile(t, "hello")); err != nil {
		t.Fatalf("uploadFile: %v", err)
	}
	if gotBody != "hello" {
		t.Fatalf("body after redirect = %q", gotBody)
	}
}

// A file that vanished after the walk, or a malformed checksum, is a local
// failure: reported, never sent, never retried.
func TestUploadFile_LocalFailures(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer srv.Close()
	c := newTestClient(t, srv)
	sleeps := recordSleeps(c)

	gone := tempCookbookFile(t, "x")
	if err := os.Remove(gone.path); err != nil {
		t.Fatal(err)
	}
	badSum := tempCookbookFile(t, "x")
	badSum.checksum = "not-hex"
	dir := tempCookbookFile(t, "x")
	dir.path = filepath.Dir(dir.path) // opens, but cannot be read as a file
	for name, f := range map[string]cookbookFile{"missing file": gone, "bad checksum": badSum, "directory": dir} {
		if err := c.uploadFile(context.Background(), srv.URL, f); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if requests != 0 || len(*sleeps) != 0 {
		t.Errorf("requests = %d, backoffs = %v; local failures must not be sent or retried", requests, *sleeps)
	}
}
