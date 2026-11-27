// Package cinc is a Go client for the Chef Infra / CINC Server API.
//
// Construct a Client with NewClient, then call methods on its service fields:
//
//	key, err := cinc.LoadKeyFile("/etc/chef/client.pem")
//	if err != nil {
//		return err
//	}
//	c, err := cinc.NewClient(cinc.Config{
//		ServerURL:  "https://chef.example.com",
//		Org:        "myorg",
//		ClientName: "node1",
//		Key:        key,
//	})
//	if err != nil {
//		return err
//	}
//	node, _, err := c.Nodes.Get(ctx, "web01")
//	if errors.Is(err, cinc.ErrNotFound) {
//		// no such node
//	}
//
// Requests are authenticated with the Chef v1.3 signed-header protocol.
package cinc
