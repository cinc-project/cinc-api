package cinc

import "context"

// RequiredRecipeService accesses the /required_recipe endpoint. Unlike the
// rest of the API this endpoint returns a Ruby recipe as text/plain rather
// than JSON.
type RequiredRecipeService struct{ client *Client }

// Get returns the configured required recipe (Ruby text). The endpoint
// returns 404 when none is configured; callers can branch with
// errors.Is(err, ErrNotFound).
func (s *RequiredRecipeService) Get(ctx context.Context) (string, *Response, error) {
	data, resp, err := s.client.doRaw(ctx, "GET",
		s.client.orgPath("/required_recipe"), nil)
	if err != nil {
		return "", resp, err
	}
	return string(data), resp, nil
}
