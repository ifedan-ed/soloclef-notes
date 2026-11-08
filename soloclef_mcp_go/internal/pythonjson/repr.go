// Python's repr(), for the values a JSON reply or a tool call's arguments hold.
//
// The vectors in the tests were taken from the running Python server, not from
// CPython's documentation.
package pythonjson

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Of renders one value the way Python's repr() would.
func Of(value Value) string {
	switch value.Kind {
	case Null:
		return "None"
	case Bool:
		if value.Bool {
			return "True"
		}
		return "False"
	case Int:
		return strconv.FormatInt(value.Int, 10)
	case Float:
		return FloatRepr(value.Float)
	case String:
		return QuoteRepr(value.String)
	case Array:
		parts := make([]string, 0, len(value.Array))
		for _, element := range value.Array {
			parts = append(parts, Of(element))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case Object:
		parts := make([]string, 0, len(value.Members))
		for _, member := range value.Members {
			parts = append(parts, QuoteRepr(member.Key)+": "+Of(member.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return "None"
}

// TypeName is the Python type name of a decoded JSON value, as pydantic names it
// in "input_type=".
func TypeName(value Value) string {
	switch value.Kind {
	case Null:
		return "NoneType"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Float:
		return "float"
	case String:
		return "str"
	case Array:
		return "list"
	case Object:
		return "dict"
	}
	return "NoneType"
}

// truncationLimit and the two slice widths are pydantic-core's, measured against
// the running Python server: a repr longer than the limit keeps its first 25
// characters and its last 24 with an ellipsis between, for a total of 52.
const (
	truncationLimit = 50
	keptFromFront   = 25
	keptFromBack    = 24
)

// Truncated is Of with pydantic's cap on how much of a value it will print.
func Truncated(value Value) string {
	return truncate(Of(value))
}

func truncate(rendered string) string {
	if len(rendered) <= truncationLimit {
		return rendered
	}
	return sliceFront(rendered, keptFromFront) + "..." + sliceBack(rendered, keptFromBack)
}

// sliceFront and sliceBack cut on a rune boundary at or inside the byte width,
// so a multi-byte character is dropped rather than halved.
func sliceFront(text string, width int) string {
	if len(text) <= width {
		return text
	}
	for width > 0 && !utf8.RuneStart(text[width]) {
		width--
	}
	return text[:width]
}

func sliceBack(text string, width int) string {
	if len(text) <= width {
		return text
	}
	start := len(text) - width
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}

// QuoteRepr renders a string as Python's repr() quotes it: single quotes unless the
// string holds a single quote and no double quote.
func QuoteRepr(text string) string {
	quote := byte('\'')
	if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
		quote = '"'
	}
	var out strings.Builder
	out.WriteByte(quote)
	for _, char := range text {
		switch {
		case char == rune(quote), char == '\\':
			out.WriteByte('\\')
			out.WriteRune(char)
		case char == '\n':
			out.WriteString(`\n`)
		case char == '\r':
			out.WriteString(`\r`)
		case char == '\t':
			out.WriteString(`\t`)
		case char < 0x20 || char == 0x7f:
			out.WriteString(fmt.Sprintf(`\x%02x`, char))
		case char < 0x80:
			out.WriteRune(char)
		case unicode.IsPrint(char):
			out.WriteRune(char)
		case char <= 0xffff:
			out.WriteString(fmt.Sprintf(`\u%04x`, char))
		default:
			out.WriteString(fmt.Sprintf(`\U%08x`, char))
		}
	}
	out.WriteByte(quote)
	return out.String()
}

// pythonExponentThreshold is where Python's repr leaves positional notation for
// exponential; Go's %g leaves it four decades later.
const pythonExponentThreshold = 17

// FloatRepr renders a float as Python's repr() prints it: the shortest form that
// round-trips, always with a point or an exponent.
func FloatRepr(number float64) string {
	if number != number { // NaN
		return "nan"
	}
	if number > 1.7976931348623157e308 {
		return "inf"
	}
	if number < -1.7976931348623157e308 {
		return "-inf"
	}
	shortest := strconv.FormatFloat(number, 'g', -1, 64)
	if exponentOf(shortest) >= pythonExponentThreshold {
		shortest = strconv.FormatFloat(number, 'e', -1, 64)
	}
	if index := strings.IndexByte(shortest, 'e'); index >= 0 {
		return shortest[:index] + pythonExponent(shortest[index+1:])
	}
	if !strings.ContainsRune(shortest, '.') {
		return shortest + ".0"
	}
	return shortest
}

// exponentOf is the decimal exponent of a %g rendering written positionally.
func exponentOf(rendered string) int {
	if strings.ContainsRune(rendered, 'e') {
		return pythonExponentThreshold
	}
	digits := strings.TrimPrefix(rendered, "-")
	point := strings.IndexByte(digits, '.')
	if point < 0 {
		return len(digits) - 1
	}
	return point - 1
}

// pythonExponent writes an exponent as Python does: a sign and at least two digits.
func pythonExponent(exponent string) string {
	sign := "+"
	if exponent != "" && (exponent[0] == '+' || exponent[0] == '-') {
		sign = string(exponent[0])
		exponent = exponent[1:]
	}
	for len(exponent) < 2 {
		exponent = "0" + exponent
	}
	return "e" + sign + exponent
}
