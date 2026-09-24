package cinc

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestNodeTags(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		n := &Node{}
		if got := n.Tags(); got != nil {
			t.Errorf("Tags() on bare node = %v, want nil", got)
		}
	})
	t.Run("nil value under tags", func(t *testing.T) {
		n := &Node{Normal: Attributes{"tags": nil}}
		if got := n.Tags(); got != nil {
			t.Errorf("Tags() = %v, want nil", got)
		}
	})
	t.Run("decoded as []any", func(t *testing.T) {
		n := &Node{Normal: Attributes{"tags": []any{"prod", "web", 7}}}
		// non-string members are skipped
		if got := n.Tags(); !slices.Equal(got, []string{"prod", "web"}) {
			t.Errorf("Tags() = %v, want [prod web]", got)
		}
	})
	t.Run("already []string", func(t *testing.T) {
		n := &Node{Normal: Attributes{"tags": []string{"a", "b"}}}
		if got := n.Tags(); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("Tags() = %v, want [a b]", got)
		}
	})
	t.Run("wrong type", func(t *testing.T) {
		n := &Node{Normal: Attributes{"tags": "not-a-list"}}
		if got := n.Tags(); got != nil {
			t.Errorf("Tags() = %v, want nil for non-array value", got)
		}
	})
}

func TestNodeSetAndMutateTags(t *testing.T) {
	t.Run("SetTags allocates normal", func(t *testing.T) {
		n := &Node{}
		n.SetTags([]string{"x"})
		if got := n.Tags(); !slices.Equal(got, []string{"x"}) {
			t.Errorf("after SetTags, Tags() = %v, want [x]", got)
		}
	})
	t.Run("AddTags dedupes and appends", func(t *testing.T) {
		n := &Node{Normal: Attributes{"tags": []any{"prod"}}}
		n.AddTags("prod", "web", "canary")
		if got := n.Tags(); !slices.Equal(got, []string{"prod", "web", "canary"}) {
			t.Errorf("after AddTags, Tags() = %v, want [prod web canary]", got)
		}
	})
	t.Run("RemoveTags drops matches", func(t *testing.T) {
		n := &Node{Normal: Attributes{"tags": []any{"prod", "web", "canary"}}}
		n.RemoveTags("web")
		if got := n.Tags(); !slices.Equal(got, []string{"prod", "canary"}) {
			t.Errorf("after RemoveTags, Tags() = %v, want [prod canary]", got)
		}
	})
}

func TestNodeRunListMutation(t *testing.T) {
	t.Run("AddRunListItems dedupes and preserves order", func(t *testing.T) {
		n := &Node{RunList: []string{"recipe[base]"}}
		n.AddRunListItems("recipe[base]", "recipe[apache]", "role[web]")
		want := []string{"recipe[base]", "recipe[apache]", "role[web]"}
		if !slices.Equal(n.RunList, want) {
			t.Errorf("RunList = %v, want %v", n.RunList, want)
		}
	})
	t.Run("RemoveRunListItems drops matches", func(t *testing.T) {
		n := &Node{RunList: []string{"recipe[base]", "recipe[apache]", "role[web]"}}
		n.RemoveRunListItems("recipe[apache]")
		want := []string{"recipe[base]", "role[web]"}
		if !slices.Equal(n.RunList, want) {
			t.Errorf("RunList = %v, want %v", n.RunList, want)
		}
	})
	t.Run("AddRunListItems on empty node", func(t *testing.T) {
		n := &Node{}
		n.AddRunListItems("recipe[a]")
		if !slices.Equal(n.RunList, []string{"recipe[a]"}) {
			t.Errorf("RunList = %v, want [recipe[a]]", n.RunList)
		}
	})
}

func TestNodeAttribute(t *testing.T) {
	n := &Node{
		Automatic: Attributes{"fqdn": "web01.example.com", "network": map[string]any{"default_gateway": "10.0.0.1"}},
		Normal:    Attributes{"role": "web", "fqdn": "shadowed"},
		Default:   Attributes{"only_default": "d"},
		Override:  Attributes{"only_override": "o"},
	}
	t.Run("automatic wins over normal", func(t *testing.T) {
		if v, ok := n.Attribute("fqdn"); !ok || v != "web01.example.com" {
			t.Errorf("Attribute(fqdn) = (%v, %v), want automatic value", v, ok)
		}
	})
	t.Run("falls through to normal", func(t *testing.T) {
		if v, ok := n.Attribute("role"); !ok || v != "web" {
			t.Errorf("Attribute(role) = (%v, %v), want web", v, ok)
		}
	})
	t.Run("falls through to default", func(t *testing.T) {
		if v, ok := n.Attribute("only_default"); !ok || v != "d" {
			t.Errorf("Attribute(only_default) = (%v, %v), want d", v, ok)
		}
	})
	t.Run("falls through to override", func(t *testing.T) {
		if v, ok := n.Attribute("only_override"); !ok || v != "o" {
			t.Errorf("Attribute(only_override) = (%v, %v), want o", v, ok)
		}
	})
	// Chef read precedence (highest -> lowest): automatic, override, normal,
	// default. These cases pin a single key into multiple scopes at once so the
	// ordering — not just fall-through — is exercised.
	t.Run("override outranks normal", func(t *testing.T) {
		n := &Node{
			Normal:   Attributes{"k": "from_normal"},
			Override: Attributes{"k": "from_override"},
		}
		if v, ok := n.Attribute("k"); !ok || v != "from_override" {
			t.Errorf("Attribute(k) = (%v, %v), want from_override", v, ok)
		}
	})
	t.Run("override outranks default", func(t *testing.T) {
		n := &Node{
			Default:  Attributes{"k": "from_default"},
			Override: Attributes{"k": "from_override"},
		}
		if v, ok := n.Attribute("k"); !ok || v != "from_override" {
			t.Errorf("Attribute(k) = (%v, %v), want from_override", v, ok)
		}
	})
	t.Run("normal outranks default", func(t *testing.T) {
		n := &Node{
			Default: Attributes{"k": "from_default"},
			Normal:  Attributes{"k": "from_normal"},
		}
		if v, ok := n.Attribute("k"); !ok || v != "from_normal" {
			t.Errorf("Attribute(k) = (%v, %v), want from_normal", v, ok)
		}
	})
	t.Run("automatic outranks override", func(t *testing.T) {
		n := &Node{
			Override:  Attributes{"k": "from_override"},
			Automatic: Attributes{"k": "from_automatic"},
		}
		if v, ok := n.Attribute("k"); !ok || v != "from_automatic" {
			t.Errorf("Attribute(k) = (%v, %v), want from_automatic", v, ok)
		}
	})
	t.Run("dotted nested path", func(t *testing.T) {
		if v, ok := n.Attribute("network.default_gateway"); !ok || v != "10.0.0.1" {
			t.Errorf("Attribute(network.default_gateway) = (%v, %v), want 10.0.0.1", v, ok)
		}
	})
	t.Run("absent", func(t *testing.T) {
		if v, ok := n.Attribute("nope"); ok {
			t.Errorf("Attribute(nope) = (%v, true), want not found", v)
		}
	})
	t.Run("nil scopes skipped", func(t *testing.T) {
		bare := &Node{Normal: Attributes{"k": "v"}}
		if v, ok := bare.Attribute("k"); !ok || v != "v" {
			t.Errorf("Attribute(k) on node with nil automatic = (%v, %v), want v", v, ok)
		}
	})
}

func TestNodeAttributeString(t *testing.T) {
	n := &Node{Automatic: Attributes{
		"fqdn":     "web01",
		"ipaddrs":  []any{"10.0.0.5", "10.0.0.6"},
		"emptylst": []any{},
		"port":     8080,
	}}
	cases := []struct {
		name, key, want string
	}{
		{name: "plain string", key: "fqdn", want: "web01"},
		{name: "first element of array", key: "ipaddrs", want: "10.0.0.5"},
		{name: "empty array", key: "emptylst", want: ""},
		{name: "non-string coerced", key: "port", want: "8080"},
		{name: "absent key", key: "missing", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := n.AttributeString(tc.key); got != tc.want {
				t.Errorf("AttributeString(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestNodeLastCheckin(t *testing.T) {
	want := time.Unix(1700000000, 500000000)
	cases := []struct {
		name   string
		node   *Node
		want   time.Time
		wantOK bool
	}{
		{"float seconds", &Node{Automatic: Attributes{"ohai_time": 1700000000.5}}, want, true},
		{"json.Number", &Node{Automatic: Attributes{"ohai_time": json.Number("1700000000.5")}}, want, true},
		{"int", &Node{Automatic: Attributes{"ohai_time": 1700000000}}, time.Unix(1700000000, 0), true},
		{"int64", &Node{Automatic: Attributes{"ohai_time": int64(1700000000)}}, time.Unix(1700000000, 0), true},
		{"no automatic attributes", &Node{}, time.Time{}, false},
		{"absent", &Node{Automatic: Attributes{"fqdn": "x"}}, time.Time{}, false},
		{"not a number", &Node{Automatic: Attributes{"ohai_time": "yesterday"}}, time.Time{}, false},
		{"bad json.Number", &Node{Automatic: Attributes{"ohai_time": json.Number("x")}}, time.Time{}, false},
		{"zero", &Node{Automatic: Attributes{"ohai_time": 0.0}}, time.Time{}, false},
		{"only in normal", &Node{Normal: Attributes{"ohai_time": 1700000000.5}}, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.node.LastCheckin()
			if ok != tc.wantOK || !got.Equal(tc.want) {
				t.Errorf("LastCheckin() = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
	t.Run("decoded from the wire", func(t *testing.T) {
		var n Node
		if err := json.Unmarshal([]byte(`{"name":"a","automatic":{"ohai_time":1700000000.5}}`), &n); err != nil {
			t.Fatal(err)
		}
		if got, ok := n.LastCheckin(); !ok || !got.Equal(want) {
			t.Errorf("LastCheckin() = (%v, %v), want (%v, true)", got, ok, want)
		}
	})
}

func TestNodeEnvironmentName(t *testing.T) {
	if got := (&Node{}).EnvironmentName(); got != "_default" {
		t.Errorf("EnvironmentName() on bare node = %q, want _default", got)
	}
	if got := (&Node{Environment: "prod"}).EnvironmentName(); got != "prod" {
		t.Errorf("EnvironmentName() = %q, want prod", got)
	}
}
