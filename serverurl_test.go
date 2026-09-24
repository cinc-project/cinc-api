package cinc

import "testing"

func TestParseServerURL(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantServer string
		wantOrg    string
		wantErr    bool
	}{
		{name: "https with org", raw: "https://chef.example.com/organizations/acme", wantServer: "https://chef.example.com", wantOrg: "acme"},
		{name: "host with port", raw: "https://chef.example.com:8443/organizations/acme", wantServer: "https://chef.example.com:8443", wantOrg: "acme"},
		{name: "trailing slash", raw: "https://chef.example.com/organizations/acme/", wantServer: "https://chef.example.com", wantOrg: "acme"},
		{name: "http scheme", raw: "http://localhost/organizations/dev", wantServer: "http://localhost", wantOrg: "dev"},
		{name: "missing scheme", raw: "chef.example.com/organizations/acme", wantErr: true},
		{name: "missing host", raw: "https:///organizations/acme", wantErr: true},
		{name: "no organizations segment", raw: "https://chef.example.com/acme", wantErr: true},
		{name: "empty org", raw: "https://chef.example.com/organizations/", wantErr: true},
		{name: "extra path segments", raw: "https://chef.example.com/organizations/acme/nodes", wantErr: true},
		{name: "no path at all", raw: "https://chef.example.com", wantErr: true},
		{name: "unparseable", raw: "://nope", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, org, err := ParseServerURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseServerURL(%q) = (%q, %q, nil), want error", tc.raw, server, org)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseServerURL(%q) unexpected error: %v", tc.raw, err)
			}
			if server != tc.wantServer || org != tc.wantOrg {
				t.Errorf("ParseServerURL(%q) = (%q, %q), want (%q, %q)", tc.raw, server, org, tc.wantServer, tc.wantOrg)
			}
		})
	}
}

func TestFormatServerURL(t *testing.T) {
	cases := []struct{ server, org, want string }{
		{"https://chef.example.com", "acme", "https://chef.example.com/organizations/acme"},
		{"https://chef.example.com/", "acme", "https://chef.example.com/organizations/acme"},
		{"https://chef.example.com:8443//", "acme", "https://chef.example.com:8443/organizations/acme"},
		{"http://localhost", "my org", "http://localhost/organizations/my%20org"},
		{"http://localhost", "a/b", "http://localhost/organizations/a%2Fb"},
	}
	for _, tc := range cases {
		if got := FormatServerURL(tc.server, tc.org); got != tc.want {
			t.Errorf("FormatServerURL(%q, %q) = %q, want %q", tc.server, tc.org, got, tc.want)
		}
	}
}

// FormatServerURL and ParseServerURL are inverses, escaping included.
func TestFormatServerURL_RoundTrip(t *testing.T) {
	for _, tc := range []struct{ server, org string }{
		{"https://chef.example.com", "acme"},
		{"https://chef.example.com:8443", "dev_1-x"},
		{"http://localhost", "my org"},
		{"http://localhost", "a/b"},
		{"http://localhost", "100%"},
	} {
		raw := FormatServerURL(tc.server, tc.org)
		server, org, err := ParseServerURL(raw)
		if err != nil {
			t.Fatalf("ParseServerURL(%q): %v", raw, err)
		}
		if server != tc.server || org != tc.org {
			t.Errorf("round trip of (%q, %q) via %q = (%q, %q)", tc.server, tc.org, raw, server, org)
		}
	}
}
