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
//	} else if err != nil {
//		return err
//	}
//	fmt.Println(node.Environment)
//
// Every non-2xx response is an *ErrorResponse that also matches a sentinel
// error (ErrNotFound, ErrConflict, ErrForbidden, ...). GETs, and any request
// the server refused with a 503, are retried with backoff (WithMaxRetries).
// Client.Do sends a signed request to any path and returns the body
// unchanged, for what the typed services do not model. The package's
// examples show the common tasks.
//
// Requests are authenticated with the Chef v1.3 signed-header protocol.
package cinc
