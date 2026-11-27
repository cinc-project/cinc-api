package cinc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

// TestCreateConflictNamesObject checks that every create which sends a named
// object records that name on the *ErrorResponse, so a caller can say which
// object already exists.
func TestCreateConflictNamesObject(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		route string
		want  string
		call  func(c *Client) error
	}{
		{"node", "POST /organizations/o/nodes", "web1", func(c *Client) error {
			_, err := c.Nodes.Create(ctx, &Node{Name: "web1"})
			return err
		}},
		{"role", "POST /organizations/o/roles", "base", func(c *Client) error {
			_, err := c.Roles.Create(ctx, &Role{Name: "base"})
			return err
		}},
		{"environment", "POST /organizations/o/environments", "prod", func(c *Client) error {
			_, err := c.Environments.Create(ctx, &Environment{Name: "prod"})
			return err
		}},
		{"client", "POST /organizations/o/clients", "web1", func(c *Client) error {
			_, _, err := c.Clients.Create(ctx, &APIClient{Name: "web1"})
			return err
		}},
		{"data bag", "POST /organizations/o/data", "secrets", func(c *Client) error {
			_, err := c.DataBags.Create(ctx, "secrets")
			return err
		}},
		{"data bag item", "POST /organizations/o/data/secrets", "db", func(c *Client) error {
			_, _, err := c.DataBags.Items("secrets").Create(ctx, DataBagItem{"id": "db"})
			return err
		}},
		{"container", "POST /organizations/o/containers", "things", func(c *Client) error {
			_, err := c.Containers.Create(ctx, "things")
			return err
		}},
		{"group", "POST /organizations/o/groups", "ops", func(c *Client) error {
			_, err := c.Groups.Create(ctx, "ops")
			return err
		}},
		{"user", "POST /users", "alice", func(c *Client) error {
			_, _, err := c.Users.Create(ctx, &User{Username: "alice"})
			return err
		}},
		{"org", "POST /organizations", "acme", func(c *Client) error {
			_, _, err := c.Orgs.Create(ctx, &Org{Name: "acme"})
			return err
		}},
		{"user key", "POST /users/alice/keys", "laptop", func(c *Client) error {
			_, _, err := c.Keys.User("alice").Create(ctx, &Key{Name: "laptop"})
			return err
		}},
		{"client key", "POST /organizations/o/clients/web1/keys", "rotated", func(c *Client) error {
			_, _, err := c.Keys.Client("web1").Create(ctx, &Key{Name: "rotated"})
			return err
		}},
		{"member", "POST /organizations/o/users", "alice", func(c *Client) error {
			_, err := c.Associations.AddMember(ctx, "alice")
			return err
		}},
		{"invite", "POST /organizations/o/association_requests", "alice", func(c *Client) error {
			_, _, err := c.Associations.Invite(ctx, "alice")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := cinctest.New(t)
			srv.Handle(tc.route, cinctest.Route{
				Status: http.StatusConflict,
				Body:   `{"error":["Object already exists"]}`,
			})
			c := newTestClient(t, srv.Server)
			err := tc.call(c)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("err = %v, want ErrConflict", err)
			}
			var er *ErrorResponse
			if !errors.As(err, &er) {
				t.Fatalf("err = %T, want *ErrorResponse", err)
			}
			if er.Name != tc.want {
				t.Errorf("Name = %q, want %q", er.Name, tc.want)
			}
		})
	}
}

// Recording the name does not change Error()'s text.
func TestCreateErrorTextUnchanged(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("POST /organizations/o/nodes", cinctest.Route{
		Status: http.StatusConflict,
		Body:   `{"error":["Node already exists"]}`,
	})
	c := newTestClient(t, srv.Server)
	_, err := c.Nodes.Create(context.Background(), &Node{Name: "web1"})
	want := "cinc: POST /organizations/o/nodes: 409: Node already exists"
	if err == nil || err.Error() != want {
		t.Fatalf("Error() = %v, want %q", err, want)
	}
}

func TestErrorResponseNameEmptyWhenUnknown(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/nodes/web1", cinctest.Route{
		Status: http.StatusNotFound,
		Body:   `{"error":["not found"]}`,
	})
	c := newTestClient(t, srv.Server)
	_, _, err := c.Nodes.Get(context.Background(), "web1")
	var er *ErrorResponse
	if !errors.As(err, &er) {
		t.Fatalf("err = %T, want *ErrorResponse", err)
	}
	if er.Name != "" {
		t.Errorf("Name = %q, want empty for a request that is not a create", er.Name)
	}
}

func TestWithName(t *testing.T) {
	if withName(nil, "x") != nil {
		t.Error("withName(nil) != nil")
	}
	plain := errors.New("boom")
	if got := withName(plain, "x"); got != plain {
		t.Errorf("withName changed a non-ErrorResponse error: %v", got)
	}
	er := &ErrorResponse{StatusCode: 409}
	wrapped := fmt.Errorf("retry: %w", er)
	if got := withName(wrapped, "web1"); got != wrapped {
		t.Errorf("withName returned %v, want the same error", got)
	}
	if er.Name != "web1" {
		t.Errorf("Name = %q, want web1 through a wrapper", er.Name)
	}
	if !errors.Is(wrapped, ErrConflict) {
		t.Error("errors.Is(ErrConflict) lost")
	}
	// An empty name leaves a name already set alone.
	if withName(er, ""); er.Name != "web1" {
		t.Errorf("Name = %q after empty withName, want web1", er.Name)
	}
}
