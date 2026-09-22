package cinc

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
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

// uploadFile PUTs raw file bytes to a signed sandbox upload URL. These URLs
// are pre-signed by the server, so the request is NOT Chef-signed; it carries
// only the Content-Type and Content-MD5 headers Chef expects.
//
// Transient failures are retried (see doTransfer) even though this is a PUT,
// which signed API calls never retry. Here repeating it is safe: the URL is
// pre-signed for one sandbox checksum, the content is addressed by that
// checksum and pinned by Content-MD5, so a repeated PUT can only store the
// same bytes again. Each attempt gets a fresh reader over data.
func (c *Client) uploadFile(ctx context.Context, uploadURL string, data []byte) error {
	sum := md5Base64(data)
	return c.doTransfer(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("cinc: build upload request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-binary")
		req.Header.Set("Content-MD5", sum)
		return req, nil
	}, func(*http.Response) error { return nil })
}
