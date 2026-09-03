package cinc

import (
	"context"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

const universeBody = `{
	"ffmpeg":{
		"0.1.0":{
			"location_path":"http://supermarket.chef.io/api/v1/cookbooks/ffmpeg/0.1.0/download",
			"location_type":"supermarket",
			"dependencies":{"git":">= 0.0.0","libvpx":"~> 0.1.1"}
		}
	},
	"pssh":{
		"0.1.0":{
			"location_path":"http://supermarket.chef.io/api/v1/cookbooks/pssh/0.1.0/download",
			"location_type":"supermarket",
			"dependencies":{}
		}
	}
}`

func TestUniverse_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/universe", cinctest.Route{Body: universeBody})
	c := newTestClient(t, srv.Server)

	u, _, err := c.Universe.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ffmpeg := u["ffmpeg"]["0.1.0"]
	if ffmpeg.LocationType != "supermarket" || ffmpeg.Dependencies["libvpx"] != "~> 0.1.1" {
		t.Fatalf("Universe = %+v", u)
	}
}

func TestUniverse_GetGlobal(t *testing.T) {
	// The global universe is top-level — must not be org-scoped.
	srv := cinctest.New(t)
	srv.Handle("GET /universe", cinctest.Route{Body: universeBody})
	c := newTestClient(t, srv.Server)

	u, _, err := c.Universe.GetGlobal(context.Background())
	if err != nil {
		t.Fatalf("GetGlobal: %v", err)
	}
	if u["pssh"]["0.1.0"].LocationPath == "" {
		t.Fatalf("Universe = %+v", u)
	}
}
