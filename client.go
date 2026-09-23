package cinc

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a Chef/CINC Server API client. It is safe for concurrent use.
type Client struct {
	baseURL    *url.URL
	baseURLStr string // cached baseURL.String() for the request hot path
	org        string
	clientName string
	key        *rsa.PrivateKey
	httpClient *http.Client
	// transferClient sends the unsigned bookshelf transfers. It is httpClient
	// with the Timeout swapped for opts.transferTimeout, sharing its
	// transport and connection pool.
	transferClient *http.Client
	opts           options
	clock          func() time.Time
	// sleep waits for d, reporting false if ctx ended first. A field so tests
	// can drive retry timing without waiting.
	sleep func(ctx context.Context, d time.Duration) bool

	// Services.
	Nodes             *NodesService
	Roles             *RolesService
	Environments      *EnvironmentsService
	Clients           *ClientsService
	DataBags          *DataBagsService
	Search            *SearchService
	Cookbooks         *CookbooksService
	CookbookArtifacts *CookbookArtifactsService
	Keys              *KeysService
	Groups            *GroupsService
	Status            *StatusService
	License           *LicenseService
	Policies          *PoliciesService
	PolicyGroups      *PolicyGroupsService
	Orgs              *OrgsService
	Users             *UsersService
	Containers        *ContainersService
	ACLs              *ACLsService
	RequiredRecipe    *RequiredRecipeService
	Associations      *AssociationsService
	Principals        *PrincipalsService
	Universe          *UniverseService
	Stats             *StatsService
}

// NewClient builds a Client from cfg and optional Options.
func NewClient(cfg Config, opts ...Option) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimRight(cfg.ServerURL, "/"))
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("cinc: invalid ServerURL %q", cfg.ServerURL)
	}
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	hc := o.httpClient
	if o.skipTLSVerify {
		// Copy the caller's client and swap only the transport, so Jar,
		// CheckRedirect and any other configuration survive. Building a fresh
		// http.Client here would silently drop them.
		clone := *hc
		clone.Transport = cloneTransportSkipVerify(hc.Transport)
		hc = &clone
	}
	// Transfers keep the caller's redirect policy (Go's default follows
	// redirects, which S3 needs across regions): they carry no signature.
	tc := *hc
	tc.Timeout = o.transferTimeout
	// Signed requests never follow a redirect. Go would forward the X-Ops-*
	// headers to the new host, which could replay the signed request for the
	// server's clock-skew window; and the signature covers the original path,
	// so the new location would reject it anyway. doOnce reports the 3xx.
	// Set on a copy, never on the caller's client.
	signed := *hc
	signed.CheckRedirect = refuseRedirect
	c := &Client{
		baseURL: base, baseURLStr: base.String(), org: cfg.Org, clientName: cfg.ClientName,
		key: cfg.Key, httpClient: &signed, transferClient: &tc, opts: o, clock: time.Now, sleep: sleepCtx,
	}
	c.Nodes = &NodesService{client: c}
	c.Roles = &RolesService{client: c}
	c.Environments = &EnvironmentsService{client: c}
	c.Clients = &ClientsService{client: c}
	c.DataBags = &DataBagsService{client: c}
	c.Search = &SearchService{client: c}
	c.Cookbooks = &CookbooksService{client: c}
	c.CookbookArtifacts = &CookbookArtifactsService{client: c}
	c.Keys = &KeysService{client: c}
	c.Groups = &GroupsService{client: c}
	c.Status = &StatusService{client: c}
	c.License = &LicenseService{client: c}
	c.Policies = &PoliciesService{client: c}
	c.PolicyGroups = &PolicyGroupsService{client: c}
	c.Orgs = &OrgsService{client: c}
	c.Users = &UsersService{client: c}
	c.Containers = &ContainersService{client: c}
	c.ACLs = &ACLsService{client: c}
	c.RequiredRecipe = &RequiredRecipeService{client: c}
	c.Associations = &AssociationsService{client: c}
	c.Principals = &PrincipalsService{client: c}
	c.Universe = &UniverseService{client: c}
	c.Stats = &StatsService{client: c}
	return c, nil
}

// refuseRedirect is the signed client's CheckRedirect: it hands the 3xx back
// to doOnce unfollowed.
func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// cloneTransportSkipVerify returns a transport that mirrors base but skips TLS
// verification. When base is a caller-supplied *http.Transport its tuning is
// preserved; otherwise (nil, as with the default client, or a non-Transport
// RoundTripper) http.DefaultTransport is cloned so HTTP/2, proxy support, and
// connection pooling are retained. Only InsecureSkipVerify is flipped on.
func cloneTransportSkipVerify(base http.RoundTripper) *http.Transport {
	tr, ok := base.(*http.Transport)
	if !ok || tr == nil {
		tr = http.DefaultTransport.(*http.Transport)
	}
	clone := tr.Clone()
	if clone.TLSClientConfig == nil {
		clone.TLSClientConfig = &tls.Config{}
	}
	clone.TLSClientConfig.InsecureSkipVerify = true
	return clone
}

// orgPath prefixes p with /organizations/<org>.
func (c *Client) orgPath(p string) string {
	return "/organizations/" + esc(c.org) + "/" + strings.TrimLeft(p, "/")
}

// sleepCtx waits for d, reporting false if ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// timestamp returns the current time as an ISO-8601 UTC string.
func (c *Client) timestamp() string {
	return c.clock().UTC().Format("2006-01-02T15:04:05Z")
}
