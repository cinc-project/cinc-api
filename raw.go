package cinc

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// Do sends a signed request to path and returns the response body exactly as
// the server sent it. path is absolute from the server root and may carry a
// query string, e.g. "/users" or "/organizations/acme/nodes/web01"; it is not
// prefixed with the client's organization. body, when non-nil, is sent as is
// with Content-Type application/json.
//
// Do is the escape hatch for callers that must not lose anything to a decode
// and re-encode — key order, number formatting, fields the typed services do
// not model — such as backup and restore tools. It retries exactly as the
// typed services do. A non-2xx response returns the body and the Response
// alongside an *ErrorResponse, so errors.Is(err, ErrNotFound) and friends
// work as usual.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) ([]byte, *Response, error) {
	return c.doRaw(ctx, method, path, body)
}

// WithServerAPIVersion returns a context whose requests ask for, and sign,
// server API version v instead of the client's default. Use it with Do for
// endpoints whose shape depends on the version.
func WithServerAPIVersion(ctx context.Context, v string) context.Context {
	return withServerAPIVersion(ctx, v)
}

// Fetch GETs an absolute URL without Chef signing, such as the pre-signed
// bookshelf URL in a cookbook manifest, and passes the response body to
// handle. Transient failures are retried as cookbook downloads are,
// including a body that is cut off mid-stream: each attempt calls handle
// again with a fresh reader from the start of the body, so handle must not
// keep state from an earlier, failed call. An error handle returns is
// retried only if it wraps a read error from the body; any other error (a
// full disk, a checksum mismatch) is returned as is.
func (c *Client) Fetch(ctx context.Context, url string, handle func(body io.Reader) error) error {
	return c.doTransfer(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("cinc: build fetch request: %w", err)
		}
		return req, nil
	}, func(resp *http.Response) error {
		return handle(wireReader{resp.Body})
	})
}
