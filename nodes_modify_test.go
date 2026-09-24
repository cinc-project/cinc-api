package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

const modifyNodeBody = `{"name":"web01","chef_environment":"prod","run_list":["recipe[nginx]"],"normal":{"tags":["prod"]}}`

func TestNodesModify(t *testing.T) {
	t.Run("sends the changed node and returns the server's copy", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/web01", cinctest.Route{Body: modifyNodeBody})
		srv.Handle("PUT /organizations/o/nodes/web01", cinctest.Route{
			Body: `{"name":"web01","chef_environment":"prod","run_list":["recipe[nginx]"],"normal":{"tags":["prod","web"]}}`,
			Assert: func(t *testing.T, _ *http.Request, body []byte) {
				var sent Node
				if err := json.Unmarshal(body, &sent); err != nil {
					t.Fatalf("PUT body: %v", err)
				}
				if sent.Name != "web01" || !slices.Equal(sent.Tags(), []string{"prod", "web"}) {
					t.Errorf("PUT body = %s", body)
				}
			},
		})
		c := newTestClient(t, srv.Server)
		n, sent, err := c.Nodes.Modify(context.Background(), "web01", func(n *Node) error {
			n.AddTags("web")
			return nil
		})
		if err != nil || !sent {
			t.Fatalf("Modify = (%v, %v, %v), want a sent update", n, sent, err)
		}
		if !slices.Equal(n.Tags(), []string{"prod", "web"}) {
			t.Errorf("returned tags = %v, want the server's [prod web]", n.Tags())
		}
	})

	t.Run("skips the PUT when nothing changed", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/web01", cinctest.Route{Body: modifyNodeBody})
		// No PUT route: the harness fails the test if one is sent.
		c := newTestClient(t, srv.Server)
		n, sent, err := c.Nodes.Modify(context.Background(), "web01", func(n *Node) error {
			n.AddTags("prod")
			n.AddRunListItems("nginx")
			return nil
		})
		if err != nil || sent {
			t.Fatalf("Modify = (%v, %v, %v), want no update", n, sent, err)
		}
		if n == nil || n.Name != "web01" || n.Environment != "prod" {
			t.Errorf("returned node = %+v, want the fetched node", n)
		}
	})

	t.Run("an error from fn aborts before the PUT", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/web01", cinctest.Route{Body: modifyNodeBody})
		c := newTestClient(t, srv.Server)
		boom := errors.New("boom")
		n, sent, err := c.Nodes.Modify(context.Background(), "web01", func(n *Node) error {
			n.AddTags("web")
			return boom
		})
		if !errors.Is(err, boom) || sent || n != nil {
			t.Fatalf("Modify = (%v, %v, %v), want (nil, false, boom)", n, sent, err)
		}
	})

	t.Run("refuses a rename", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/web01", cinctest.Route{Body: modifyNodeBody})
		c := newTestClient(t, srv.Server)
		n, sent, err := c.Nodes.Modify(context.Background(), "web01", func(n *Node) error {
			n.Name = "web02"
			return nil
		})
		if err == nil || sent || n != nil || !strings.Contains(err.Error(), "web02") {
			t.Fatalf("Modify = (%v, %v, %v), want a rename error", n, sent, err)
		}
	})

	t.Run("reports an unencodable change", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/web01", cinctest.Route{Body: modifyNodeBody})
		c := newTestClient(t, srv.Server)
		n, sent, err := c.Nodes.Modify(context.Background(), "web01", func(n *Node) error {
			n.Normal["bad"] = make(chan int)
			return nil
		})
		if err == nil || sent || n != nil {
			t.Fatalf("Modify = (%v, %v, %v), want an encoding error", n, sent, err)
		}
	})

	t.Run("a nil fn is an error", func(t *testing.T) {
		c := newTestClient(t, cinctest.New(t).Server)
		if _, _, err := c.Nodes.Modify(context.Background(), "web01", nil); err == nil {
			t.Fatal("Modify with nil fn: want an error")
		}
	})

	t.Run("a failed Get is returned", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/web01", cinctest.Route{Status: 404, Body: `{"error":["not found"]}`})
		c := newTestClient(t, srv.Server)
		called := false
		_, sent, err := c.Nodes.Modify(context.Background(), "web01", func(*Node) error {
			called = true
			return nil
		})
		if !errors.Is(err, ErrNotFound) || sent || called {
			t.Fatalf("Modify = (%v, %v), called=%v; want ErrNotFound without calling fn", sent, err, called)
		}
	})

	t.Run("a failed PUT is returned and counted as sent", func(t *testing.T) {
		srv := cinctest.New(t)
		srv.Handle("GET /organizations/o/nodes/web01", cinctest.Route{Body: modifyNodeBody})
		srv.Handle("PUT /organizations/o/nodes/web01", cinctest.Route{Status: 403, Body: `{"error":["no"]}`})
		c := newTestClient(t, srv.Server)
		n, sent, err := c.Nodes.Modify(context.Background(), "web01", func(n *Node) error {
			n.Environment = "staging"
			return nil
		})
		if !errors.Is(err, ErrForbidden) || !sent || n != nil {
			t.Fatalf("Modify = (%v, %v, %v), want (nil, true, ErrForbidden)", n, sent, err)
		}
	})
}
