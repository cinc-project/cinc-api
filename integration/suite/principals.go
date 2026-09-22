package suite

import (
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func testPrincipals(t *testing.T, tgt Target, c *cinc.Client) {
	ctx := t.Context()
	for name, wantType := range map[string]string{newClient(t, c): "client", tgt.Admin: "user"} {
		ps, _, err := c.Principals.Get(ctx, name)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		var found *cinc.Principal
		for i := range ps {
			if ps[i].Type == wantType {
				found = &ps[i]
			}
		}
		if found == nil {
			t.Fatalf("principals for %s = %+v, want one of type %q", name, ps, wantType)
		}
		if found.Name != name || found.PublicKey == "" || !found.OrgMember {
			t.Errorf("principal = %+v, want name %s, a public key and org membership", found, name)
		}
	}
}
