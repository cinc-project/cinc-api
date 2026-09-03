// Package integration drives the public cinc-api client against an in-memory
// cinc-zero Chef Infra Server. Unlike the unit tests (which use the cinctest
// fake), these exercise the full signed-request path end to end against a real
// server implementation: Mixlib v1.3 auth verification, JSON wire formats,
// status codes, and the error mapping in do[T].
package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/tas50/cinc-zero/server"
)

// newClient starts an in-memory cinc-zero server (org "test", auth on) and
// returns a cinc client authenticated as the bootstrap admin. The server is
// torn down on test cleanup.
func newClient(t *testing.T) *cinc.Client {
	t.Helper()
	srv, err := server.New(server.Options{Orgs: []string{"test"}})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("server.Start: %v", err)
	}

	// Use a client whose transport we retain so cleanup can close pooled
	// keep-alive connections before stopping the server. Otherwise the server's
	// graceful shutdown blocks until our idle connections time out — several
	// seconds per test after a parallel cookbook download opens many of them.
	hc := &http.Client{Timeout: 30 * time.Second}
	t.Cleanup(func() {
		hc.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})

	key, err := cinc.ParseKey(srv.AdminKey())
	if err != nil {
		t.Fatalf("ParseKey admin key: %v", err)
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL:  srv.URL(),
		Org:        "test",
		ClientName: srv.AdminName(),
		Key:        key,
	}, cinc.WithHTTPClient(hc))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestIntegration_Status(t *testing.T) {
	c := newClient(t)
	if _, _, err := c.Status.Get(context.Background()); err != nil {
		t.Fatalf("Status.Get: %v", err)
	}
}

func TestIntegration_NodeLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	// A real Chef server answers POST /nodes with {"uri": "..."} rather than
	// the full node object, so the returned value is intentionally not asserted
	// on here — creation is verified through the subsequent Get.
	if _, _, err := c.Nodes.Create(ctx, &cinc.Node{
		Name: "web01", Environment: "_default", RunList: []string{"recipe[nginx]"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _, err := c.Nodes.Get(ctx, "web01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.RunList) != 1 || got.RunList[0] != "recipe[nginx]" {
		t.Fatalf("run_list = %v, want [recipe[nginx]]", got.RunList)
	}

	got.RunList = append(got.RunList, "recipe[base]")
	if _, _, err := c.Nodes.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if again, _, err := c.Nodes.Get(ctx, "web01"); err != nil || len(again.RunList) != 2 {
		t.Fatalf("after update: run_list=%v err=%v", again.RunList, err)
	}

	list, _, err := c.Nodes.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := list["web01"]; !ok {
		t.Fatalf("web01 missing from node list: %v", list)
	}

	if _, err := c.Nodes.Delete(ctx, "web01"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// A real server returns 404, which do[T] maps to the ErrNotFound sentinel.
	if _, _, err := c.Nodes.Get(ctx, "web01"); !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_Search(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, _, err := c.Nodes.Create(ctx, &cinc.Node{Name: name, RunList: []string{}}); err != nil {
			t.Fatalf("seed node %s: %v", name, err)
		}
	}
	res, _, err := c.Search.Query(ctx, "node", "*:*")
	if err != nil {
		t.Fatalf("Search.Query: %v", err)
	}
	if res.Total < 3 {
		t.Fatalf("search total = %d, want >= 3", res.Total)
	}
}

func TestIntegration_NotFound(t *testing.T) {
	c := newClient(t)
	_, _, err := c.Nodes.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, cinc.ErrNotFound) {
		t.Fatalf("Get missing node: err = %v, want ErrNotFound", err)
	}
}
