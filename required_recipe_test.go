package cinc

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestRequiredRecipe_Get(t *testing.T) {
	const body = "log 'hello from required recipe'\n"
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/required_recipe", cinctest.Route{Body: body})
	c := newTestClient(t, srv.Server)

	got, _, err := c.RequiredRecipe.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != body {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestRequiredRecipe_NotConfigured(t *testing.T) {
	// When no required recipe is configured the server returns 404. Surface
	// that as ErrNotFound so callers can branch on errors.Is(err, ErrNotFound).
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/required_recipe",
		cinctest.Route{Status: http.StatusNotFound, Body: "Required recipe is not configured\n"})
	c := newTestClient(t, srv.Server)

	_, _, err := c.RequiredRecipe.Get(context.Background())
	if err == nil {
		t.Fatal("expected error when no recipe configured")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound chain", err)
	}
}

func TestRequiredRecipe_PreservesNonJSONBody(t *testing.T) {
	// The endpoint returns text/plain Ruby — must not be JSON-decoded.
	const ruby = "package 'nginx'\nservice 'nginx' do\n  action :start\nend\n"
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/required_recipe", cinctest.Route{Body: ruby})
	c := newTestClient(t, srv.Server)

	got, _, err := c.RequiredRecipe.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != ruby {
		t.Errorf("body altered:\ngot  %q\nwant %q", got, ruby)
	}
}
