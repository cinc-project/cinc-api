package cinc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
)

// sandbox is the server response to a sandbox creation request.
type sandbox struct {
	ID        string                     `json:"sandbox_id"`
	Checksums map[string]sandboxChecksum `json:"checksums"`
}

// sandboxChecksum describes whether a single file must be uploaded.
type sandboxChecksum struct {
	NeedsUpload bool   `json:"needs_upload"`
	URL         string `json:"url"`
}

// createSandbox registers a set of MD5-hex checksums and learns which the
// server still needs uploaded.
func (c *Client) createSandbox(ctx context.Context, checksumsHex []string) (*sandbox, *Response, error) {
	set := make(map[string]any, len(checksumsHex))
	for _, ck := range checksumsHex {
		set[ck] = nil
	}
	sb, resp, err := do[sandbox](ctx, c, "POST", c.orgPath("/sandboxes"),
		map[string]any{"checksums": set})
	return ptrOrNil(sb, err), resp, err
}

// commitSandbox finalizes a sandbox after all needed files are uploaded.
func (c *Client) commitSandbox(ctx context.Context, id string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, c, "PUT",
		c.orgPath("/sandboxes/"+esc(id)), map[string]any{"is_completed": true})
	return resp, err
}

// uploadFile PUTs a cookbook file to a signed sandbox upload URL. These URLs
// are pre-signed by the server, so the request is NOT Chef-signed; it carries
// only the Content-Type and Content-MD5 headers Chef expects.
//
// The file is streamed from f.path rather than held in memory, and
// Content-MD5 is derived from f.checksum, the MD5 taken when the cookbook was
// walked, so the file is read once here and not hashed again. If the file
// changed since, the server rejects the mismatched digest rather than storing
// the wrong bytes under that checksum.
//
// Transient failures are retried (see doTransfer) even though this is a PUT,
// which signed API calls never retry. Here repeating it is safe: the URL is
// pre-signed for one sandbox checksum, the content is addressed by that
// checksum and pinned by Content-MD5, so a repeated PUT can only store the
// same bytes again. Each attempt reopens the file and sends it from the start.
func (c *Client) uploadFile(ctx context.Context, uploadURL string, f cookbookFile) error {
	sum, err := md5HexToBase64(f.checksum)
	if err != nil {
		return fmt.Errorf("cinc: upload checksum: %w", err)
	}
	return c.doTransfer(ctx, func() (*http.Request, error) {
		body, size, err := openUploadBody(f.path)
		if err != nil {
			return nil, fmt.Errorf("cinc: open upload: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, body)
		if err != nil {
			_ = body.Close() // read-only
			return nil, fmt.Errorf("cinc: build upload request: %w", err)
		}
		// net/http cannot size a file body itself and would send it chunked,
		// which S3-backed bookshelves reject; the length must be explicit.
		// GetBody lets a redirected PUT (S3 issues 307s) resend the body, as a
		// bytes.Reader body did implicitly.
		req.ContentLength = size
		req.GetBody = func() (io.ReadCloser, error) {
			b, _, err := openUploadBody(f.path)
			return b, err
		}
		req.Header.Set("Content-Type", "application/x-binary")
		req.Header.Set("Content-MD5", sum)
		return req, nil
	}, func(*http.Response) error { return nil })
}

// openUploadBody opens the regular file at path as an upload body and returns
// its size. An empty file is http.NoBody, since net/http reads a zero
// ContentLength on any other body as "unknown" and would send it chunked.
func openUploadBody(path string) (io.ReadCloser, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", path)
	}
	if err != nil || info.Size() == 0 {
		_ = file.Close() // read-only
		if err != nil {
			return nil, 0, err
		}
		return http.NoBody, 0, nil
	}
	return file, info.Size(), nil
}
