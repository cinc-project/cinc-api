package suite

import (
	"strings"
	"testing"
)

func TestCheckGaps(t *testing.T) {
	names := []string{"nodes/lifecycle", "status/get"}
	cases := []struct {
		desc    string
		gaps    map[string]string
		wantErr string
	}{
		{"no gaps", nil, ""},
		{"known test with a reason", map[string]string{"nodes/lifecycle": "cinc-server-ng#1"}, ""},
		{"unknown test", map[string]string{"nodes/lifecyle": "typo"}, `"nodes/lifecyle" names no test`},
		{"empty reason", map[string]string{"status/get": " "}, `"status/get" has no reason`},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			err := checkGaps(names, c.gaps)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("checkGaps: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("checkGaps error = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestCaseNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range cases {
		if seen[tc.name] {
			t.Errorf("duplicate case name %q", tc.name)
		}
		seen[tc.name] = true
	}
}

func TestUniqueName(t *testing.T) {
	a, b := uniqueName(t, "node"), uniqueName(t, "node")
	if a == b {
		t.Fatalf("uniqueName returned %q twice", a)
	}
	if !strings.HasPrefix(a, "t-node-") || len(a) != len("t-node-")+8 {
		t.Fatalf("uniqueName = %q, want t-node-<8 hex>", a)
	}
}
