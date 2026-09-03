package cinc

import (
	"context"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

func TestLicense_Get(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /license", cinctest.Route{
		Body: `{"limit_exceeded":false,"node_license":25,"node_count":5,"upgrade_url":"https://www.chef.io/contact-us"}`,
	})
	c := newTestClient(t, srv.Server)

	lic, _, err := c.License.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if lic.LimitExceeded {
		t.Errorf("LimitExceeded = true, want false")
	}
	if lic.NodeLicense != 25 || lic.NodeCount != 5 {
		t.Errorf("license counts = %+v", lic)
	}
	if lic.UpgradeURL == "" {
		t.Errorf("UpgradeURL is empty")
	}
}

func TestLicense_LimitExceeded(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /license", cinctest.Route{
		Body: `{"limit_exceeded":true,"node_license":25,"node_count":42,"upgrade_url":"https://www.chef.io/contact-us"}`,
	})
	c := newTestClient(t, srv.Server)
	lic, _, err := c.License.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !lic.LimitExceeded {
		t.Error("LimitExceeded = false, want true")
	}
	if lic.NodeCount <= lic.NodeLicense {
		t.Errorf("expected NodeCount (%d) to exceed NodeLicense (%d)", lic.NodeCount, lic.NodeLicense)
	}
}

func TestLicense_Error(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /license", cinctest.Route{
		Status: 500, Body: `{"error":["license server unavailable"]}`,
	})
	c := newTestClient(t, srv.Server)
	if _, _, err := c.License.Get(context.Background()); err == nil {
		t.Fatal("expected error from 500")
	}
}
