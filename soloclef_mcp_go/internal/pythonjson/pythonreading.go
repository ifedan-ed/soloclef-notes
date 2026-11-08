package pythonjson

// The readers here mirror how the Python renderers read a backend reply, which
// is with dict.get and an f-string. Reading more carefully than that would be a
// different result: where a reply carries a null title, Python prints "None",
// and where it carries a number, Python prints the number.

// Text is str(value.get(key, fallback)): the member rendered as Python's str(),
// or the fallback when the member is absent.
func Text(value Value, key, fallback string) string {
	member, present := value.Lookup(key)
	if !present {
		return fallback
	}
	return PythonText(member)
}

// PythonText is Python's str(): a string as it stands, anything else as its repr.
func PythonText(value Value) string {
	if value.Kind == String {
		return value.String
	}
	return Of(value)
}

// Truthy is Python's truth test: a non-empty string or container, a non-zero
// number, true, and not null.
func Truthy(value Value) bool {
	switch value.Kind {
	case Null:
		return false
	case Bool:
		return value.Bool
	case Int:
		return value.Int != 0
	case Float:
		return value.Float != 0
	case String:
		return value.String != ""
	case Array:
		return len(value.Array) > 0
	case Object:
		return len(value.Members) > 0
	}
	return false
}

// TruthyText is the member's str() when the member is present and truthy, and
// otherwise nothing — the shape of "if row.get('meta'): lines.append(row['meta'])".
func TruthyText(value Value, key string) string {
	member, present := value.Lookup(key)
	if !present || !Truthy(member) {
		return ""
	}
	return PythonText(member)
}

// Number is value.get(key, fallback) read as an integer, with a float or a
// numeric string accepted as the Python renderers' format strings accept them.
func Number(value Value, key string, fallback int) int {
	member, present := value.Lookup(key)
	if !present {
		return fallback
	}
	switch member.Kind {
	case Int:
		return int(member.Int)
	case Float:
		return int(member.Float)
	default:
		return fallback
	}
}

// Decimal is value.get(key, fallback) read as a float, for a score printed with
// a fixed number of places.
func Decimal(value Value, key string, fallback float64) float64 {
	member, present := value.Lookup(key)
	if !present {
		return fallback
	}
	switch member.Kind {
	case Int:
		return float64(member.Int)
	case Float:
		return member.Float
	default:
		return fallback
	}
}

// Members is value.get(key, []) read as an array.
func Members(value Value, key string) []Value {
	member, present := value.Lookup(key)
	if !present || member.Kind != Array {
		return nil
	}
	return member.Array
}
