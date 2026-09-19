// attributes_test.go
package cinc

import (
	"encoding/json"
	"testing"
)

func TestAttributes_DigAndGet(t *testing.T) {
	a := Attributes{"kernel": map[string]any{"machine": "x86_64", "bits": float64(64)}}
	if v, ok := a.Dig("kernel", "machine"); !ok || v != "x86_64" {
		t.Fatalf("Dig = %v %v", v, ok)
	}
	if got := a.GetString("kernel", "machine"); got != "x86_64" {
		t.Fatalf("GetString = %q", got)
	}
	if _, ok := a.Dig("kernel", "missing"); ok {
		t.Fatal("expected miss")
	}
}

func TestAttributes_Dig_EmptyPath(t *testing.T) {
	a := Attributes{"x": 1}
	if v, ok := a.Dig(); ok || v != nil {
		t.Fatalf("Dig() with no args = (%v, %v), want (nil, false)", v, ok)
	}
}

func TestAttributes_Dig_NonMapIntermediate(t *testing.T) {
	// "kernel.machine" is a string, so digging further must return (nil, false)
	// rather than panicking on the wrong type assertion.
	a := Attributes{"kernel": map[string]any{"machine": "x86_64"}}
	if v, ok := a.Dig("kernel", "machine", "deeper"); ok || v != nil {
		t.Fatalf("Dig into non-map = (%v, %v), want (nil, false)", v, ok)
	}
}

func TestAttributes_Dig_MissingTopLevel(t *testing.T) {
	a := Attributes{"kernel": map[string]any{}}
	if v, ok := a.Dig("absent"); ok || v != nil {
		t.Fatalf("Dig(absent) = (%v, %v), want (nil, false)", v, ok)
	}
}

func TestAttributes_GetString_NonString(t *testing.T) {
	// Non-string values must yield "" (not a panic, not a stringification).
	a := Attributes{"port": float64(8080), "enabled": true, "name": "web"}
	if got := a.GetString("port"); got != "" {
		t.Errorf("GetString(port) = %q, want \"\"", got)
	}
	if got := a.GetString("enabled"); got != "" {
		t.Errorf("GetString(enabled) = %q, want \"\"", got)
	}
	if got := a.GetString("name"); got != "web" {
		t.Errorf("GetString(name) = %q, want web", got)
	}
}

func TestAttributes_GetString_Missing(t *testing.T) {
	if got := (Attributes{}).GetString("nope"); got != "" {
		t.Errorf("GetString on missing key = %q, want \"\"", got)
	}
}

func TestAttributes_RoundTripPreservesUnknown(t *testing.T) {
	src := []byte(`{"a":1,"nested":{"keep":true},"x":["y"]}`)
	var a Attributes
	if err := json.Unmarshal(src, &a); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	json.Unmarshal(out, &got)
	json.Unmarshal(src, &want)
	if len(got) != len(want) || got["a"] != want["a"] {
		t.Fatalf("round trip lost data: %s", out)
	}
}

// A nested attribute tree built in Go holds Attributes values, not
// map[string]any. Dig must walk those too: a type assertion to map[string]any
// does not match the named type.
func TestDig_TraversesNestedAttributes(t *testing.T) {
	a := Attributes{"network": Attributes{"interfaces": Attributes{"eth0": "up"}}}
	got, ok := a.Dig("network", "interfaces", "eth0")
	if !ok || got != "up" {
		t.Errorf("Dig = %v, %v; want \"up\", true", got, ok)
	}
	if got := a.GetString("network", "interfaces", "eth0"); got != "up" {
		t.Errorf("GetString = %q, want \"up\"", got)
	}
}

func TestNodeAttribute_TraversesNestedAttributes(t *testing.T) {
	n := &Node{Normal: Attributes{"network": Attributes{"default_gateway": "10.0.0.1"}}}
	if got := n.AttributeString("network.default_gateway"); got != "10.0.0.1" {
		t.Errorf("AttributeString = %q, want \"10.0.0.1\"", got)
	}
}
