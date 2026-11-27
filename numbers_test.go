package cinc

import (
	"encoding/json"
	"strings"
	"testing"
)

// 2^53 + 1: the smallest integer a float64 cannot hold.
const bigInt = "9007199254740993"

// Attribute and data bag numbers decode as json.Number, so a read-modify-write
// (Nodes.Modify, Get then Update) sends back exactly what the server stored
// instead of rounding every integer above 2^53.
func TestNumbers_RoundTripLosslessly(t *testing.T) {
	t.Run("node attributes", func(t *testing.T) {
		var n Node
		if err := json.Unmarshal([]byte(`{"name":"n","normal":{"limits":{"nofile":`+bigInt+`}},"automatic":{"uptime":1.5}}`), &n); err != nil {
			t.Fatal(err)
		}
		if v, _ := n.Normal.Dig("limits", "nofile"); v != json.Number(bigInt) {
			t.Errorf("nofile = %#v, want json.Number(%s)", v, bigInt)
		}
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"nofile":` + bigInt, `"uptime":1.5`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("re-encoded %s, want %s", b, want)
			}
		}
	})
	t.Run("role and environment attributes", func(t *testing.T) {
		var r Role
		var e Environment
		body := `{"name":"x","default_attributes":{"n":` + bigInt + `},"override_attributes":{"n":` + bigInt + `}}`
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatal(err)
		}
		for _, a := range []Attributes{r.DefaultAttributes, r.OverrideAttributes, e.DefaultAttributes, e.OverrideAttributes} {
			if a["n"] != json.Number(bigInt) {
				t.Errorf("n = %#v, want json.Number", a["n"])
			}
		}
	})
	t.Run("policy revision attributes", func(t *testing.T) {
		var p PolicyRevision
		if err := json.Unmarshal([]byte(`{"name":"p","default_attributes":{"n":`+bigInt+`},"override_attributes":{"n":`+bigInt+`}}`), &p); err != nil {
			t.Fatal(err)
		}
		if p.DefaultAttributes["n"] != json.Number(bigInt) || p.OverrideAttributes["n"] != json.Number(bigInt) {
			t.Errorf("attributes = %#v / %#v, want json.Number", p.DefaultAttributes["n"], p.OverrideAttributes["n"])
		}
	})
	t.Run("data bag item", func(t *testing.T) {
		var item DataBagItem
		if err := json.Unmarshal([]byte(`{"id":"x","n":`+bigInt+`,"list":[`+bigInt+`]}`), &item); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"id":"x","list":[`+bigInt+`],"n":`+bigInt+`}` {
			t.Errorf("re-encoded %s", b)
		}
	})
	t.Run("null leaves the map nil", func(t *testing.T) {
		a := Attributes{"stale": 1}
		if err := json.Unmarshal([]byte(`null`), &a); err != nil || a != nil {
			t.Errorf("Attributes from null = %#v, %v; want nil", a, err)
		}
		d := DataBagItem{"stale": 1}
		if err := json.Unmarshal([]byte(`null`), &d); err != nil || d != nil {
			t.Errorf("DataBagItem from null = %#v, %v; want nil", d, err)
		}
	})
	t.Run("not an object", func(t *testing.T) {
		var a Attributes
		var d DataBagItem
		if json.Unmarshal([]byte(`[1]`), &a) == nil || json.Unmarshal([]byte(`"x"`), &d) == nil {
			t.Error("want an error decoding a non-object")
		}
	})
}

// An encrypted item fetched from the server decodes with json.Number too, so
// its wrappers' "version" is a json.Number; it must still decrypt, and a big
// integer inside an encrypted value must come back intact.
func TestNumbers_EncryptedItemFromTheWire(t *testing.T) {
	secret := []byte("s3cr3t")
	plain := DataBagItem{"id": "x", "n": json.Number(bigInt), "nested": map[string]any{"m": json.Number(bigInt)}}
	enc, err := plain.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	var fetched DataBagItem
	if err := json.Unmarshal(wire, &fetched); err != nil {
		t.Fatal(err)
	}
	if !fetched.IsEncrypted() {
		t.Fatal("IsEncrypted = false for a fetched encrypted item")
	}
	dec, err := fetched.Decrypt(secret)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec["n"] != json.Number(bigInt) {
		t.Errorf("n = %#v, want json.Number(%s)", dec["n"], bigInt)
	}
	if m, _ := dec["nested"].(map[string]any); m["m"] != json.Number(bigInt) {
		t.Errorf("nested.m = %#v, want json.Number(%s)", dec["nested"], bigInt)
	}
}

// A wrapper's version is a json.Number when decoded from the server and a
// float64 or int when built in Go; anything else, or a non-integer, is not a
// version.
func TestWrapperVersion(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want int
		ok   bool
	}{
		{json.Number("3"), 3, true},
		{float64(2), 2, true},
		{1, 1, true},
		{json.Number("3.5"), 0, false},
		{3.5, 3, false},
		{"3", 0, false},
		{nil, 0, false},
	} {
		got, ok := wrapperVersion(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("wrapperVersion(%#v) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
