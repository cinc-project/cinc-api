package cinc

import "context"

// SuperuserName is the Chef Server's built-in superuser, "pivotal". Some
// server-wide operations are reserved to it rather than to any org admin or
// server-admin: creating organizations, POST /authenticate_user, and
// associating a user with an org without an invitation. Its key lives on the
// server (/etc/opscode/pivotal.pem on erchef).
const SuperuserName = "pivotal"

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
	// Create sends it whenever PublicKey is empty, so it need not be set.
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

// Create creates a new user. Unless u.PublicKey is set, the server generates
// the user's "default" keypair and the result's ChefKey.PrivateKey carries the
// private half, the only chance to capture it: under server API v1 a user
// created with neither create_key nor public_key gets no key at all, so
// create_key is sent whenever no public key is. u is not modified.
func (s *UsersService) Create(ctx context.Context, u *User) (*UserCreateResult, *Response, error) {
	req := *u
	if req.PublicKey == "" {
		req.CreateKey = true
	}
	r, resp, err := do[UserCreateResult](ctx, s.client, "POST", "/users", &req)
	return ptrOrNil(r, err), resp, err
}

// Update replaces a user's metadata. Use UserName as the lookup key; other
// fields are the new values.
func (s *UsersService) Update(ctx context.Context, u *User) (*User, *Response, error) {
	updated, resp, err := do[User](ctx, s.client, "PUT", "/users/"+esc(u.UserName), u)
	return ptrOrNil(updated, err), resp, err
}

// userKeyFields are the fields a user GET may carry that erchef rejects on a
// user PUT under API v1 (key_management_not_supported): keys change through
// Keys.User. cinc-server-ng returns public_key on a GET.
var userKeyFields = []string{"public_key", "private_key", "create_key", "chef_key"}

// SetPassword sets a user's password. erchef's user PUT requires display_name
// and (for a locally authenticated user) email, and replaces the email with
// whatever it is sent, so a password alone is not a valid update: this GETs
// the user and PUTs back everything the server returned, less key fields,
// with the new password. It is not atomic, so a concurrent update to the same
// user between the two calls is overwritten. Erchef wants at least six
// characters.
func (s *UsersService) SetPassword(ctx context.Context, name, password string) (*Response, error) {
	path := "/users/" + esc(name)
	user, resp, err := do[map[string]any](ctx, s.client, "GET", path, nil)
	if err != nil {
		return resp, err
	}
	for _, k := range userKeyFields {
		delete(user, k)
	}
	user["password"] = password
	_, resp, err = do[map[string]any](ctx, s.client, "PUT", path, user)
	return resp, err
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
