package cinc

import "context"

// UniverseEntry describes one cookbook version in the universe: where to fetch
// it and what it depends on.
type UniverseEntry struct {
	LocationPath string            `json:"location_path,omitempty"`
	LocationType string            `json:"location_type,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// Universe is the known collection of cookbooks, keyed by cookbook name and
// then by version string. It feeds Berkshelf/Supermarket dependency
// resolution.
type Universe map[string]map[string]UniverseEntry

// UniverseService accesses the /universe endpoint, which exists both
// org-scoped and at the top level.
type UniverseService struct{ client *Client }

// Get returns the org-scoped universe (/organizations/ORG/universe).
func (s *UniverseService) Get(ctx context.Context) (Universe, *Response, error) {
	return do[Universe](ctx, s.client, "GET", s.client.orgPath("/universe"), nil)
}
