// crud_test.go
package cinc

import (
	"context"
	"testing"

	"github.com/cinc-project/cinc-api/internal/cinctest"
)

type widget struct {
	Name string `json:"name"`
}

func TestCrud_GetAndList(t *testing.T) {
	srv := cinctest.New(t)
	srv.Handle("GET /organizations/o/widgets/w1",
		cinctest.Route{Body: `{"name":"w1"}`})
	srv.Handle("GET /organizations/o/widgets",
		cinctest.Route{Body: `{"w1":"http://x/w1"}`})
	c := newTestClient(t, srv.Server)
	r := crud[widget]{client: c, path: "/widgets"}

	got, _, err := r.get(context.Background(), "w1")
	if err != nil || got.Name != "w1" {
		t.Fatalf("get: %+v %v", got, err)
	}
	names, _, err := r.list(context.Background())
	if err != nil || names["w1"] == "" {
		t.Fatalf("list: %+v %v", names, err)
	}
}
