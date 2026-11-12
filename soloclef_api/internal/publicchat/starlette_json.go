package publicchat

import (
	"bytes"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// sqlalchemyPrefix is the prefix of the keys FastAPI's jsonable_encoder
// drops from every dict it walks.
//
// A route with no response_model hands what it returns to jsonable_encoder
// before Starlette renders it, and jsonable_encoder is called with
// `sqlalchemy_safe=True`: any *string* key starting with "_sa" is left out,
// at every depth, on the theory that it is SQLAlchemy's instance state. The
// video and podcast routes return values straight out of jsonb, so a stored
// key such as "_sa_notes" is silently missing from their answers. A key that
// is not a string is never dropped, whatever it would be spelled as.
//
// The page route is not subject to this: it has a response_model, and
// pydantic serialises it without jsonable_encoder ever seeing it.
const sqlalchemyPrefix = "_sa"

// renderUnmodelled renders what a route without a response_model returned:
// jsonable_encoder's walk, and then Starlette's
// json.dumps(ensure_ascii=False, allow_nan=False, separators=(",", ":")).
//
// It exists beside pythonjson.Marshal, rather than calling it on the whole
// value, for two things that function has no shape for: the dropped "_sa"
// keys, and a dict whose keys are not strings (python_value.go's dict), which
// json.dumps writes by coercing each key to text -- so a dict keyed by both 1
// and "1" is rendered with the name "1" twice. Every scalar is still written
// by pythonjson, so there is one float repr and one string escaping.
//
// An error is a value json.dumps refuses: a float that is not finite, as a
// value or as a key. That is raised while the response is rendered, after the
// route returned, so it is a 500 with no CORS headers like any other.
func renderUnmodelled(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := writeUnmodelled(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeUnmodelled(out *bytes.Buffer, value any) error {
	switch held := value.(type) {
	case []any:
		out.WriteByte('[')
		for position, item := range held {
			if position > 0 {
				out.WriteByte(',')
			}
			if err := writeUnmodelled(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
		return nil
	case *pythonjson.Object:
		keys := make([]any, 0, held.Len())
		values := make([]any, 0, held.Len())
		for _, key := range held.Keys() {
			keys = append(keys, key)
			values = append(values, held.GetOrNil(key))
		}
		return writeMembers(out, keys, values)
	case *dict:
		return writeMembers(out, held.keys, held.values)
	}
	scalar, err := pythonjson.Marshal(value)
	if err != nil {
		return err
	}
	out.Write(scalar)
	return nil
}

// writeMembers writes one dict: every member whose key jsonable_encoder
// keeps, in order, with the key as json.dumps spells it.
func writeMembers(out *bytes.Buffer, keys, values []any) error {
	out.WriteByte('{')
	written := 0
	for position, key := range keys {
		if text, isText := key.(string); isText && strings.HasPrefix(text, sqlalchemyPrefix) {
			continue
		}
		name, err := memberName(key)
		if err != nil {
			return err
		}
		if written > 0 {
			out.WriteByte(',')
		}
		written++
		out.Write(name)
		out.WriteByte(':')
		if err := writeUnmodelled(out, values[position]); err != nil {
			return err
		}
	}
	out.WriteByte('}')
	return nil
}

// memberName is how json.dumps writes a dict key. A string is itself. The
// other types a JSON value can be are coerced, each in its own way:
//
//	an int   -> its digits                  {1: …}    -> "1"
//	a float  -> float.__repr__              {1e16: …} -> "1e+16"
//	a bool   -> the JSON word, not Python's {True: …} -> "true"
//	None     -> "null"
//
// which is the scalar's own JSON text inside quotes in every case, so the
// scalar is rendered and wrapped. A float key that is not finite is refused,
// as a float value is.
func memberName(key any) ([]byte, error) {
	if text, isText := key.(string); isText {
		return pythonjson.Marshal(text)
	}
	if whole, isInt := key.(pythonjson.RawInt); isInt {
		// As the digits Python's int prints, not the column's own text.
		number, _ := wholeNumber(string(whole))
		return []byte(`"` + number.String() + `"`), nil
	}
	scalar, err := pythonjson.Marshal(key)
	if err != nil {
		return nil, err
	}
	return append(append([]byte{'"'}, scalar...), '"'), nil
}
