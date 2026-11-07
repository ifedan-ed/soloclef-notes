// Package pythonjson writes JSON exactly as the Python API tier writes it, so a
// route served by Go returns the same bytes as the route it replaces.
//
// Starlette's JSONResponse renders with
// json.dumps(content, ensure_ascii=False, allow_nan=False, separators=(",", ":")),
// which differs from encoding/json in three ways that reach the wire: it never
// escapes <, > or &, it never escapes U+2028/U+2029, and a dict keeps its
// insertion order rather than being sorted.
package pythonjson

// Object is a JSON object that keeps insertion order, as a Python dict does.
type Object struct {
	keys   []string
	values map[string]any
}

func NewObject() *Object {
	return &Object{values: map[string]any{}}
}

// Set appends a new key, or replaces the value of one already present without
// moving it — Python dict assignment semantics.
func (o *Object) Set(key string, value any) {
	if _, seen := o.values[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *Object) Get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

// GetString is the equivalent of dict.get(key, fallback) for a string value: a
// value of another type counts as absent, as a caller reading a text field in
// free-form metadata means.
func (o *Object) GetString(key, fallback string) string {
	if v, ok := o.values[key]; ok {
		if s, isString := v.(string); isString {
			return s
		}
	}
	return fallback
}

// GetOrNil is dict.get(key) with no fallback: a missing key is JSON null.
func (o *Object) GetOrNil(key string) any {
	return o.values[key]
}

func (o *Object) Keys() []string {
	return o.keys
}

func (o *Object) Len() int {
	return len(o.keys)
}
