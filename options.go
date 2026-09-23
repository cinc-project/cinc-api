package cinc

import (
	"crypto/rsa"
	"errors"
	"net/http"
	"time"
)

// Config holds the required parameters for constructing a Client.
type Config struct {
	ServerURL  string          // e.g. https://chef.example.com
	Org        string          // organization short name
	ClientName string          // Chef client or user name
	Key        *rsa.PrivateKey // RSA private key for request signing
}

func (c Config) validate() error {
	switch {
	case c.ServerURL == "":
		return errors.New("cinc: Config.ServerURL is required")
	case c.Org == "":
		return errors.New("cinc: Config.Org is required")
	case c.ClientName == "":
		return errors.New("cinc: Config.ClientName is required")
	case c.Key == nil:
		return errors.New("cinc: Config.Key is required")
	}
	return nil
}

type options struct {
	httpClient    *http.Client
	userAgent     string
	chefVersion   string
	skipTLSVerify bool
	maxRetries    int
	// transferTimeout bounds each attempt at a bookshelf file transfer. It
	// is separate from httpClient.Timeout, which covers API calls.
	transferTimeout time.Duration
}

func defaultOptions() options {
	return options{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		userAgent:   "cinc-api-go",
		chefVersion: "16.0.0",
		maxRetries:  2,
		// Generous enough for a large cookbook file on a slow link, while
		// still ending a transfer that has stalled outright.
		transferTimeout: 10 * time.Minute,
	}
}

// Option customizes a Client.
type Option func(*options)

// WithHTTPClient sets a custom *http.Client.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) {
		if c != nil {
			o.httpClient = c
		}
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(o *options) { o.userAgent = ua } }

// WithChefVersion sets the X-Chef-Version header.
func WithChefVersion(v string) Option { return func(o *options) { o.chefVersion = v } }

// WithSkipTLSVerify disables TLS certificate verification (testing only).
func WithSkipTLSVerify(skip bool) Option {
	return func(o *options) { o.skipTLSVerify = skip }
}

// WithMaxRetries sets how many times a failed request is retried: a GET after
// a 5xx or a network error, any other method after a 503. The default is 2.
func WithMaxRetries(n int) Option {
	return func(o *options) {
		if n >= 0 {
			o.maxRetries = n
		}
	}
}

// WithTransferTimeout bounds each attempt at a cookbook file transfer (the
// unsigned bookshelf GETs and sandbox PUTs behind Cookbooks.Upload/Download
// and CookbookArtifacts.Upload), including reading or sending the whole body.
// It replaces the http.Client's own Timeout for those requests only; API
// calls keep it. The default is 10 minutes; 0 removes the limit, leaving the
// request context as the only bound. Negative values are ignored.
func WithTransferTimeout(d time.Duration) Option {
	return func(o *options) {
		if d >= 0 {
			o.transferTimeout = d
		}
	}
}
