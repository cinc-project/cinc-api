package cinc

import (
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
	opts       options
	clock      func() time.Time

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
	c := &Client{
		baseURL: base, baseURLStr: base.String(), org: cfg.Org, clientName: cfg.ClientName,
		key: cfg.Key, httpClient: hc, opts: o, clock: time.Now,
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
	return "/organizations/" + c.org + "/" + strings.TrimLeft(p, "/")
}

// timestamp returns the current time as an ISO-8601 UTC string.
func (c *Client) timestamp() string {
	return c.clock().UTC().Format("2006-01-02T15:04:05Z")
}
