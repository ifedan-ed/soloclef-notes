package pythonjson

import "strings"

// NewObject builds an object whose members stay in the order written, which is how
// the Python originals build a request body.
func NewObject(members ...Member) Value {
	return Value{Kind: Object, Members: members}
}

// Field pairs a name with a value.
func Field(name string, value Value) Member { return Member{Key: name, Value: value} }

// Str is a string value.
func Str(text string) Value { return Value{Kind: String, String: text} }

// Num is an integer value.
func Num(number int) Value { return Value{Kind: Int, Int: int64(number)} }

// Flag is a boolean value.
func Flag(flag bool) Value { return Value{Kind: Bool, Bool: flag} }

// Nothing is JSON null.
func Nothing() Value { return Value{Kind: Null} }

// Strings is an array of strings; a nil slice is null, which is how an omitted
// optional list reaches the backend.
func Strings(texts []string) Value {
	if texts == nil {
		return Nothing()
	}
	elements := make([]Value, 0, len(texts))
	for _, text := range texts {
		elements = append(elements, Str(text))
	}
	return Value{Kind: Array, Array: elements}
}

// OptionalNum is an integer value, or null when the caller omitted it.
func OptionalNum(number int, given bool) Value {
	if !given {
		return Nothing()
	}
	return Num(number)
}

// OptionalStr is a string value, or null when the caller gave none.
func OptionalStr(text string, given bool) Value {
	if !given {
		return Nothing()
	}
	return Str(text)
}

// Compact renders a value with no whitespace, for a request body.
func Compact(value Value) string {
	var out strings.Builder
	writeCompact(&out, value)
	return out.String()
}

func writeCompact(out *strings.Builder, value Value) {
	switch value.Kind {
	case Array:
		out.WriteByte('[')
		for index, element := range value.Array {
			if index > 0 {
				out.WriteByte(',')
			}
			writeCompact(out, element)
		}
		out.WriteByte(']')
	case Object:
		out.WriteByte('{')
		for index, member := range value.Members {
			if index > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quote(member.Key))
			out.WriteByte(':')
			writeCompact(out, member.Value)
		}
		out.WriteByte('}')
	default:
		var scalar strings.Builder
		writeValue(&scalar, value, 0, 0)
		out.WriteString(scalar.String())
	}
}
