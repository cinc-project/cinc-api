package cinc

// Attributes is a free-form Chef attribute tree. It marshals as a plain JSON
// object and preserves any keys it does not interpret.
type Attributes map[string]any

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
