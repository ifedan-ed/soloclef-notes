package pythonjson

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Indented renders a value as Python's json.dumps(indent=2, ensure_ascii=False)
// does, which is the form every tool that returns JSON returns.
//
// Python's encoder keeps a dict's insertion order, prints a float with Python's
// repr, and leaves non-ASCII characters alone; Go's own encoder sorts map keys,
// prints floats in its own shortest form, and escapes HTML. Each difference
// would show in a tool result, so the rendering is written out here.
func Indented(value Value, indent int) string {
	var out strings.Builder
	writeValue(&out, value, indent, 0)
	return out.String()
}

func writeValue(out *strings.Builder, value Value, indent, depth int) {
	switch value.Kind {
	case Null:
		out.WriteString("null")
	case Bool:
		out.WriteString(strconv.FormatBool(value.Bool))
	case Int:
		out.WriteString(strconv.FormatInt(value.Int, 10))
	case Float:
		out.WriteString(FloatRepr(value.Float))
	case String:
		out.WriteString(quote(value.String))
	case Array:
		writeArray(out, value, indent, depth)
	case Object:
		writeObject(out, value, indent, depth)
	}
}

func writeArray(out *strings.Builder, value Value, indent, depth int) {
	if len(value.Array) == 0 {
		out.WriteString("[]")
		return
	}
	out.WriteString("[\n")
	for index, element := range value.Array {
		if index > 0 {
			out.WriteString(",\n")
		}
		out.WriteString(pad(indent, depth+1))
		writeValue(out, element, indent, depth+1)
	}
	out.WriteString("\n" + pad(indent, depth) + "]")
}

func writeObject(out *strings.Builder, value Value, indent, depth int) {
	if len(value.Members) == 0 {
		out.WriteString("{}")
		return
	}
	out.WriteString("{\n")
	for index, member := range value.Members {
		if index > 0 {
			out.WriteString(",\n")
		}
		out.WriteString(pad(indent, depth+1))
		out.WriteString(quote(member.Key))
		out.WriteString(": ")
		writeValue(out, member.Value, indent, depth+1)
	}
	out.WriteString("\n" + pad(indent, depth) + "}")
}

func pad(indent, depth int) string { return strings.Repeat(" ", indent*depth) }

// quote escapes a string the way Python's json encoder does with
// ensure_ascii=False: the two mandatory escapes, the five short control escapes,
// \u00XX for the rest of C0, and every other character written as it stands.
func quote(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, char := range text {
		switch char {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if char < 0x20 {
				out.WriteString(`\u00`)
				const hexDigits = "0123456789abcdef"
				out.WriteByte(hexDigits[(char>>4)&0xf])
				out.WriteByte(hexDigits[char&0xf])
				continue
			}
			out.WriteRune(char)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// FromGo turns a Go value into an ordered tree, keeping the member order of an
// Object it is given and otherwise marshalling as Go would.
func FromGo(value any) (Value, error) {
	if ordered, ok := value.(Value); ok {
		return ordered, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return Value{}, err
	}
	return DecodeAny(raw)
}
