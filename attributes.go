package cinc

import (
	"bytes"
	"encoding/json"
)

// Attributes is a free-form Chef attribute tree. It marshals as a plain JSON
// object and preserves any keys it does not interpret.
//
// Numbers decode as json.Number rather than float64, at every depth, so an
// integer above 2^53 (a file-descriptor limit, an inode count, an ID) is sent
// back exactly as the server stored it after a read-modify-write. Use
// json.Number's Int64 or Float64 to read one.
type Attributes map[string]any

// UnmarshalJSON decodes an attribute object, numbers as json.Number.
func (a *Attributes) UnmarshalJSON(b []byte) error {
	m, err := decodeObject(b)
	if err != nil {
		return err
	}
	*a = m
	return nil
}

// decodeObject decodes a JSON object with numbers as json.Number. null
// decodes to a nil map.
func decodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// Dig walks nested maps along path and returns the value at the leaf.
//
// It traverses both map[string]any (what encoding/json produces) and
// Attributes (what a tree built in Go holds).
func (a Attributes) Dig(path ...string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	var cur any = a
	for _, key := range path {
		m, ok := asAttributeMap(cur)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// asAttributeMap unwraps the two shapes a nested attribute level can take. A
// type assertion matches the dynamic type exactly, so map[string]any alone
// would miss an Attributes value even though the two share an underlying type.
func asAttributeMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case Attributes:
		return m, true
	case map[string]any:
		return m, true
	default:
		return nil, false
	}
}

// GetString returns the string at path, or "" if absent or not a string.
func (a Attributes) GetString(path ...string) string {
	if v, ok := a.Dig(path...); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
