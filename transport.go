package cinc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cinc-project/cinc-api/internal/signing"
)

// doRaw sends a signed request and returns the raw response body.
// The caller owns closing nothing — the body is fully read and closed here.
func (c *Client) doRaw(ctx context.Context, method, path string, body []byte) ([]byte, *Response, error) {
	var attempt int
	for {
		data, resp, err := c.doOnce(ctx, method, path, body)
		// Retry only transient failures: a 5xx response, or a genuine transport
		// error (no HTTP response at all). A non-2xx response surfaces as a
		// non-nil err *with* resp set, so gate the network-error check on
		// resp == nil — otherwise every 4xx (not-found, forbidden, ...) would be
		// retried, since isNetErr treats any non-context error as retriable.
		serverErr := resp != nil && resp.StatusCode >= 500
		netErr := resp == nil && isNetErr(err)
		if (serverErr || netErr) && method == http.MethodGet && attempt < c.opts.maxRetries {
			attempt++
			continue
		}
		return data, resp, err
	}
}

func isNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

func (c *Client) doOnce(ctx context.Context, method, path string, body []byte) ([]byte, *Response, error) {
	u := c.baseURLStr + path
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: build request: %w", err)
	}
	// Strip the query string from the path used for signing only; the v1.3
	// signing spec requires the canonical path to exclude the query string.
	signPath := path
	if i := strings.IndexByte(path, '?'); i >= 0 {
		signPath = path[:i]
	}
	hdrs, err := signing.SignHeaders(signing.Request{
		Method: method, Path: signPath, Body: body,
		UserID: c.clientName, Timestamp: c.timestamp(),
	}, c.key)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range hdrs {
		req.Header[k] = v
	}
	req.Header.Set("X-Chef-Version", c.opts.chefVersion)
	req.Header.Set("User-Agent", c.opts.userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: %s %s: %w", method, path, err)
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: read body: %w", err)
	}
	resp := &Response{HTTPResponse: httpResp, StatusCode: httpResp.StatusCode}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return data, resp, newErrorResponse(method, path, httpResp.StatusCode, data)
	}
	return data, resp, nil
}

// do sends a signed request, decoding a 2xx JSON body into T.
func do[T any](ctx context.Context, c *Client, method, path string, body any) (T, *Response, error) {
	var zero T
	var encoded []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return zero, nil, fmt.Errorf("cinc: marshal body: %w", err)
		}
		encoded = b
	}
	data, resp, err := c.doRaw(ctx, method, path, encoded)
	if err != nil {
		return zero, resp, err
	}
	var out T
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return zero, resp, fmt.Errorf("cinc: decode response: %w", err)
		}
	}
	return out, resp, nil
}
