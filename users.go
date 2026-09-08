package cinc

import "context"

// User is a global Chef Server user account. These live at /users (not under
// any one org) and represent humans who can be added to organizations.
type User struct {
	UserName    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	MiddleName  string `json:"middle_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`

	// Password is sent only on create/update; never returned.
	Password string `json:"password,omitempty"`

	// CreateKey, when true on create, asks the server to generate the user's
	// default keypair. The response then carries ChefKey with PrivateKey set.
	CreateKey bool `json:"create_key,omitempty"`

	// PublicKey may be supplied on create to set the default key without
	// having the server generate one. Mutually exclusive with CreateKey.
	PublicKey string `json:"public_key,omitempty"`
}

// UserCreateResult is the response from POST /users. The ChefKey.PrivateKey
// is returned **only** when the user is created with CreateKey=true; capture
// it before the response is dropped.
type UserCreateResult struct {
	URI     string  `json:"uri,omitempty"`
	ChefKey ChefKey `json:"chef_key"`
}

// UsersService accesses the top-level /users endpoints.
type UsersService struct{ client *Client }

// List returns the username -> URL index of every user. Typically restricted
// to the pivotal superuser identity.
func (s *UsersService) List(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET", "/users", nil)
}

// Get retrieves a single user's metadata by name.
func (s *UsersService) Get(ctx context.Context, name string) (*User, *Response, error) {
	u, resp, err := do[User](ctx, s.client, "GET", "/users/"+esc(name), nil)
	return ptrOrNil(u, err), resp, err
}

// Create creates a new user. With CreateKey=true the response carries the
// generated private key, which is the only chance to capture it.
func (s *UsersService) Create(ctx context.Context, u *User) (*UserCreateResult, *Response, error) {
	r, resp, err := do[UserCreateResult](ctx, s.client, "POST", "/users", u)
	return ptrOrNil(r, err), resp, err
}

// Update replaces a user's metadata. Use UserName as the lookup key; other
// fields are the new values.
func (s *UsersService) Update(ctx context.Context, u *User) (*User, *Response, error) {
	updated, resp, err := do[User](ctx, s.client, "PUT", "/users/"+esc(u.UserName), u)
	return ptrOrNil(updated, err), resp, err
}

// Delete removes a user.
func (s *UsersService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE", "/users/"+esc(name), nil)
	return resp, err
}

// Authenticate verifies a user's username and password against the top-level
// /authenticate_user endpoint. A nil error means the credentials are valid; a
// 401 is reported as an error wrapping ErrUnauthorized.
func (s *UsersService) Authenticate(ctx context.Context, username, password string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "POST", "/authenticate_user",
		map[string]string{"username": username, "password": password})
	return resp, err
}
