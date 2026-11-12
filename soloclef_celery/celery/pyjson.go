package celery

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Celery's envelopes are written by Python's json.dumps through kombu, and
// Python's defaults are not Go's: a member is separated by ", " and a key from
// its value by ": ", and every rune outside printable ASCII is escaped
// (ensure_ascii). The spelling matters here because an envelope this worker
// publishes is compared byte for byte against one Celery published
// (publish_test.go) — that comparison is the only evidence that what goes on
// the queue is what a Python worker expects to find there.
//
// Everything below covers the JSON types an envelope carries: null, bool,
// string, integer, float, array and object. Anything else is an error rather
// than a guess.

// pyField is one member of an object, with its value already encoded.
type pyField struct {
	key   string
	value string
}

// pyObject is a JSON object in the order its fields were added, which is the
// order Python writes a dict in.
type pyObject []pyField

func (o pyObject) add(key, value string) pyObject {
	return append(o, pyField{key: key, value: value})
}

func (o pyObject) encode() string {
	var out strings.Builder
	out.WriteByte('{')
	for i, field := range o {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(pyString(field.key))
		out.WriteString(": ")
		out.WriteString(field.value)
	}
	out.WriteByte('}')
	return out.String()
}

// pyJSON encodes a value as Python's json.dumps would.
func pyJSON(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "null", nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case string:
		return pyString(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			encoded, err := pyJSON(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, encoded)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case Mapping:
		// An object whose key order is the call site's rather than sorted
		// (ordered_mapping.go).
		return v.json()
	case map[string]any:
		object := pyObject{}
		for _, key := range sortedKeys(v) {
			encoded, err := pyJSON(v[key])
			if err != nil {
				return "", err
			}
			object = object.add(key, encoded)
		}
		return object.encode(), nil
	default:
		return "", fmt.Errorf("celery: cannot publish a %T", value)
	}
}

// pyString escapes as json.dumps does with its default ensure_ascii: the two
// mandatory escapes, Python's five short forms, and \u for everything else
// outside printable ASCII.
func pyString(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			switch {
			case r >= 0x20 && r < 0x7f:
				out.WriteRune(r)
			case r > 0xffff:
				high, low := utf16.EncodeRune(r)
				fmt.Fprintf(&out, `\u%04x\u%04x`, high, low)
			case r == utf8.RuneError:
				// A byte that is not UTF-8 at all. Python raises here; this
				// worker publishes the replacement character rather than
				// refuse a task name it was given.
				out.WriteString(`�`)
			default:
				fmt.Fprintf(&out, `\u%04x`, r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// pyRepr is Python's repr, which is what Celery stores in the argsrepr and
// kwargsrepr headers. Nothing reads them but a human and Flower, and they are
// reproduced because the envelope is compared byte for byte.
func pyRepr(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "None", nil
	case pyTuple:
		return pyReprTuple(v)
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case string:
		return pyReprString(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			encoded, err := pyRepr(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, encoded)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case Mapping:
		return v.repr()
	case map[string]any:
		parts := make([]string, 0, len(v))
		for _, key := range sortedKeys(v) {
			encoded, err := pyRepr(v[key])
			if err != nil {
				return "", err
			}
			parts = append(parts, pyReprString(key)+": "+encoded)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		return "", fmt.Errorf("celery: cannot describe a %T", value)
	}
}

// pyReprTuple is repr of a tuple, down to the trailing comma a tuple of one
// carries: `.delay(7)` packs its arguments into a tuple, so "(7,)" is what
// Celery writes where apply_async(args=[7]) writes "[7]".
func pyReprTuple(values pyTuple) (string, error) {
	parts := make([]string, 0, len(values))
	for _, item := range values {
		encoded, err := pyRepr(item)
		if err != nil {
			return "", err
		}
		parts = append(parts, encoded)
	}
	if len(parts) == 1 {
		return "(" + parts[0] + ",)", nil
	}
	return "(" + strings.Join(parts, ", ") + ")", nil
}

// pyReprString quotes the way Python does: single quotes, unless the string
// holds one and no double quote.
func pyReprString(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var out strings.Builder
	out.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote):
			out.WriteByte('\\')
			out.WriteRune(r)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(&out, `\x%02x`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte(quote)
	return out.String()
}

// sortedKeys orders an object's keys so the same value always encodes the same
// way. Python writes a dict in insertion order, which a Go map does not have;
// no envelope this worker publishes carries more than one key per object, so
// the two cannot disagree in practice.
func sortedKeys(of map[string]any) []string {
	keys := make([]string, 0, len(of))
	for key := range of {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
