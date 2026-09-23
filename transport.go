package cinc

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cinc-project/cinc-api/internal/signing"
)

// retryBaseDelay is the wait before the first retry; each subsequent attempt
// doubles it. With the default maxRetries of 2 a failing GET adds at most
// 300ms, which is enough to let a server finish a restart or a load balancer
// pick a different backend — the point of retrying at all.
const retryBaseDelay = 100 * time.Millisecond

// maxRetryAfter caps the wait a 503's Retry-After header can ask for, so a
// server (or a proxy in front of it) cannot stall a caller for minutes.
const maxRetryAfter = 10 * time.Second

// doRaw sends a signed request and returns the raw response body.
// The caller owns closing nothing — the body is fully read and closed here.
//
// Every attempt goes through doOnce, so a retry resends the same body and is
// signed afresh with a new timestamp.
func (c *Client) doRaw(ctx context.Context, method, path string, body []byte) ([]byte, *Response, error) {
	for attempt := 0; ; attempt++ {
		data, resp, err := c.doOnce(ctx, method, path, body)
		status := 0
		var hdr http.Header
		if resp != nil {
			status = resp.StatusCode
			hdr = resp.HTTPResponse.Header
		}
		if !retryable(method, status, err) || !c.backoff(ctx, attempt, c.retryAfter(status, hdr)) {
			return data, resp, err
		}
	}
}

// retryable reports whether a signed request that ended with status and err
// may be sent again. A GET may repeat any transient failure (shouldRetry).
// Any other method may repeat only a 503: the server refused the request
// without processing it (erchef answers 503 when its key-generation pool is
// empty), whereas a 500, 502 or 504 may follow a create or delete that was
// applied, and a wire failure may have happened after the server acted.
func retryable(method string, status int, err error) bool {
	if method == http.MethodGet {
		return shouldRetry(status, err)
	}
	return status == http.StatusServiceUnavailable
}

// shouldRetry reports whether a failed attempt is transient: a 5xx response,
// or a failure on the wire. status is 0 when no HTTP response arrived. A
// non-2xx response surfaces as an error too, which is why the status is
// checked rather than the error alone — otherwise every 4xx (not-found,
// forbidden, ...) would look like a failure worth repeating.
func shouldRetry(status int, err error) bool {
	return status >= 500 || isRetriable(err)
}

// backoff waits before retry number attempt (0-based), doubling the delay
// each time, or for atLeast if that is longer (see retryAfter). It reports
// false, without waiting, once maxRetries is spent, or if ctx ends during the
// wait: either way the caller should give up.
func (c *Client) backoff(ctx context.Context, attempt int, atLeast time.Duration) bool {
	if attempt >= c.opts.maxRetries {
		return false
	}
	return c.sleep(ctx, max(retryBaseDelay<<attempt, atLeast))
}

// retryAfter returns the wait a 503 response's Retry-After header asks for,
// either delay-seconds or an HTTP date, capped at maxRetryAfter. It returns
// 0 for any other status and for a missing, unparseable or past value.
func (c *Client) retryAfter(status int, h http.Header) time.Duration {
	if status != http.StatusServiceUnavailable {
		return 0
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = at.Sub(c.clock())
	}
	return min(max(d, 0), maxRetryAfter)
}

// doTransfer sends an unsigned request to a pre-signed bookshelf URL, the
// file-transfer half of cookbook upload and download. It must never carry
// Chef signing headers, so it bypasses doOnce and goes through
// transferClient, whose timeout is WithTransferTimeout rather than the API
// client's.
//
// Transient failures are retried with the same policy as signed GETs:
// shouldRetry, backoff and WithMaxRetries. newReq is called once per attempt
// so each one sends its body from the start. handle consumes a 2xx response;
// an error it returns is retried only if it wraps a transportErr, so a
// failure reading the body is retried while a local disk error is not.
func (c *Client) doTransfer(ctx context.Context, newReq func() (*http.Request, error), handle func(*http.Response) error) error {
	for attempt := 0; ; attempt++ {
		status, hdr, err := c.transferOnce(newReq, handle)
		if !shouldRetry(status, err) || !c.backoff(ctx, attempt, c.retryAfter(status, hdr)) {
			return err
		}
	}
}

// transferOnce makes one doTransfer attempt, returning the response status
// (0 if none arrived) and headers (nil if none arrived) alongside any error.
func (c *Client) transferOnce(newReq func() (*http.Request, error), handle func(*http.Response) error) (int, http.Header, error) {
	req, err := newReq()
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.transferClient.Do(req)
	if err != nil {
		return 0, nil, &transportErr{fmt.Errorf("cinc: bookshelf %s: %w", req.Method, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, newErrorResponse(req.Method, req.URL.String(), resp.StatusCode, body)
	}
	return resp.StatusCode, resp.Header, handle(resp)
}

// wireReader marks read errors from a response body as transport failures,
// so doTransfer can tell a connection that died mid-body from a failure on
// the consuming side.
type wireReader struct{ r io.Reader }

func (w wireReader) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if err != nil && err != io.EOF {
		err = &transportErr{err}
	}
	return n, err
}

// transportErr marks a failure that happened on the wire, as opposed to a
// client-side one — an unbuildable request, a signing failure — which would
// fail identically however many times it is repeated.
type transportErr struct{ err error }

func (e *transportErr) Error() string { return e.err.Error() }
func (e *transportErr) Unwrap() error { return e.err }

// isRetriable reports whether err is a wire failure a GET may safely repeat.
// Context cancellation and deadlines never retry: the caller is done waiting.
// Neither does a failed TLS handshake that will fail the same way next time.
func isRetriable(err error) bool {
	var te *transportErr
	if !errors.As(err, &te) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return !isPermanentTLSError(err)
}

// isPermanentTLSError reports whether err is a TLS failure that repeating the
// request cannot fix: a certificate that failed verification (unknown
// authority, wrong hostname, expired or otherwise invalid, or no roots to
// verify against), or a server that is not speaking TLS on an https:// URL.
// Chef's own client makes the same call for "certificate verify failed".
func isPermanentTLSError(err error) bool {
	// net/http replaces the tls.RecordHeaderError of a plain-HTTP reply with
	// an unwrapped errors.New, so the message is all there is to match.
	if strings.Contains(err.Error(), "server gave HTTP response to HTTPS client") {
		return true
	}
	var (
		verifyErr  *tls.CertificateVerificationError
		authErr    x509.UnknownAuthorityError
		hostErr    x509.HostnameError
		invalidErr x509.CertificateInvalidError
		rootsErr   x509.SystemRootsError
		headerErr  tls.RecordHeaderError
	)
	return errors.As(err, &verifyErr) || errors.As(err, &authErr) ||
		errors.As(err, &hostErr) || errors.As(err, &invalidErr) ||
		errors.As(err, &rootsErr) || errors.As(err, &headerErr)
}

// apiVersionKey is the context key withServerAPIVersion stores under.
type apiVersionKey struct{}

// withServerAPIVersion makes requests sent with ctx ask for (and sign) server
// API version v instead of signing.ServerAPIVersion. It exists for the few
// requests whose body only a newer API version accepts — see uploadCookbook.
func withServerAPIVersion(ctx context.Context, v string) context.Context {
	return context.WithValue(ctx, apiVersionKey{}, v)
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
	apiVersion, _ := ctx.Value(apiVersionKey{}).(string)
	hdrs, err := signing.SignHeaders(signing.Request{
		Method: method, Path: signPath, Body: body,
		UserID: c.clientName, Timestamp: c.timestamp(),
		APIVersion: apiVersion,
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
		return nil, nil, &transportErr{fmt.Errorf("cinc: %s %s: %w", method, path, err)}
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, nil, &transportErr{fmt.Errorf("cinc: read body: %w", err)}
	}
	resp := &Response{HTTPResponse: httpResp, StatusCode: httpResp.StatusCode}
	if httpResp.StatusCode >= 300 && httpResp.StatusCode < 400 {
		return data, resp, redirectError(method, path, httpResp)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return data, resp, newErrorResponse(method, path, httpResp.StatusCode, data)
	}
	return data, resp, nil
}

// redirectError reports a 3xx to a signed request, which the client does not
// follow (see refuseRedirect). It names the Location, resolved against the
// request URL, so a misconfigured server URL or proxy is easy to spot.
func redirectError(method, path string, r *http.Response) *ErrorResponse {
	where := "no Location"
	if loc, err := r.Location(); err == nil {
		where = loc.String()
	} else if raw := r.Header.Get("Location"); raw != "" {
		where = raw
	}
	return &ErrorResponse{
		Method: method, Path: path, StatusCode: r.StatusCode,
		Messages: []string{"the server redirected to " + where +
			", and signed requests do not follow redirects; check the server URL"},
	}
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
