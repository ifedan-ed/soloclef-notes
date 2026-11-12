package searchconnectors

import (
	"bytes"
	"errors"
	"math"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is what SQLAlchemy hands a route for a `json` column, and what
// pydantic-core then writes for a value of any JSON type.
//
// `search_source_connectors.config` is `json`, not `jsonb`. Postgres keeps the
// text of a `json` value exactly as it was written, so everything the two
// tiers could disagree about is still in it when it is read: the order of the
// keys, a key written twice, `1.50`, an integer of any length -- and an escape
// that names half of a surrogate pair, which `jsonb` would have refused and
// `json` only checks the spelling of.

// errIntegerTooLong is json.loads refusing an integer of more than 4,300
// digits ("Exceeds the limit (4300 digits) for integer string conversion").
// SQLAlchemy calls json.loads as it fetches the row, so this fails the
// *statement* that read it, whatever the route would have done with the row.
var errIntegerTooLong = errors.New("searchconnectors: an integer in a json column is longer than Python reads")

// errUnrenderable is pydantic-core refusing to write a str that holds a lone
// surrogate. It is raised while FastAPI serialises what the route *returned*,
// which is after the route's own `try` has ended -- so unlike every other
// failure of these reads it is not the route's 500 but Starlette's, and
// carries no CORS headers.
var errUnrenderable = errors.New("searchconnectors: a value holding a lone surrogate cannot be rendered")

// maxIntegerDigits is sys.int_info.default_max_str_digits.
const maxIntegerDigits = 4300

// errTooDeepToLoad is json.loads raising RecursionError. Like the integer
// above it is raised as the row is fetched, so it fails the statement.
var errTooDeepToLoad = errors.New("searchconnectors: a json column is nested deeper than Python reads")

// maxLoadedDepth is how many containers, one inside the next, json.loads
// reads before it raises -- the outermost counted.
//
// **This number is measured, not derived, and it is an accident of two
// things.** CPython 3.12 allows 10,000 nested C calls, and the json scanner
// spends one per container; SQLAlchemy calls json.loads from eight deep in
// that budget. So 9,992 was read and 9,993 was not, four runs out of four.
// Postgres itself stores a `json` value nested past 16,000, so the limit is
// Python's and a row can be on either side of it -- by direct SQL only:
// nothing in the app can write a value this deep, because json.dumps gives up
// sooner (trust_route.go).
//
// It is a mirror of an accident and is not exact at its edge. The eight is
// the depth of one call stack, measured through a recording harness run
// against the Python version before it was retired; a server that drove it
// from another event loop may stand a frame or two elsewhere, and the
// neighbouring limit in trust_route.go was seen to move by one between two
// Python processes. So the rows that hold this to Python's answers
// (testdata/connections_seed.sql, 820 and 821) sit either side of it with
// room to spare, and a config within a few containers of the number is the
// one stored value for which this tier's answer is a best measurement rather
// than the Python tier's own.
const maxLoadedDepth = 9992

// readColumn is json.loads of a column's text: nil, bool, string,
// pythonjson.RawInt, float64, []any or *pythonjson.Object.
//
// pythonjson.Decode is that already, for everything but three things a
// `json` column can hold, and each of them is a different status somewhere:
//
//   - An integer of more than 4,300 digits is a ValueError in Python, raised
//     while the row is fetched.
//   - `-0` is the integer zero, and is written back as `0`.
//   - An escape naming a lone surrogate (`"\ud800"`) is a str holding that
//     surrogate. encoding/json quietly reads it as U+FFFD, which would turn
//     Python's 500 into a 200 with a replacement character in it. Here the
//     surrogate survives the decoding (carryLoneSurrogates) and is held in
//     the string as the three bytes it would be if UTF-8 encoded surrogates
//     -- which is not valid UTF-8, so nothing else in a decoded value can be
//     mistaken for one, and every place a Python str behaves differently for
//     holding a surrogate can ask (surrogates.go).
func readColumn(raw []byte) (any, error) {
	text, carried := carryLoneSurrogates(raw)
	value, err := pythonjson.Decode(text)
	if err != nil {
		return nil, err
	}
	return asPythonLoads(value, carried, 1)
}

// asPythonLoads applies the rules above to a decoded value. `level` is how
// many containers deep a container met here would be.
func asPythonLoads(value any, carried bool, level int) (any, error) {
	switch value.(type) {
	case []any, *pythonjson.Object:
		if level > maxLoadedDepth {
			return nil, errTooDeepToLoad
		}
	}
	switch held := value.(type) {
	case pythonjson.RawInt:
		digits := strings.TrimPrefix(string(held), "-")
		if len(digits) > maxIntegerDigits {
			return nil, errIntegerTooLong
		}
		if held == "-0" {
			return pythonjson.RawInt("0"), nil
		}
		return held, nil
	case string:
		if carried {
			return uncarry(held), nil
		}
		return held, nil
	case []any:
		for index, item := range held {
			read, err := asPythonLoads(item, carried, level+1)
			if err != nil {
				return nil, err
			}
			held[index] = read
		}
		return held, nil
	case *pythonjson.Object:
		// Rebuilt rather than edited, because a key can carry a surrogate
		// too. The carrying is one-to-one, so two keys that differed before
		// it differ after it and none is lost.
		read := pythonjson.NewObject()
		for _, key := range held.Keys() {
			item, _ := held.Get(key)
			converted, err := asPythonLoads(item, carried, level+1)
			if err != nil {
				return nil, err
			}
			if carried {
				key = uncarry(key)
			}
			read.Set(key, converted)
		}
		return read, nil
	}
	return value, nil
}

// The carrying. A lone surrogate cannot pass through a JSON decoder, so each
// one is rewritten, in the column's text, as an escape character followed by
// a character that names it; the escape character itself, wherever the text
// already had one, is doubled. After decoding, uncarry turns each pair back:
// a doubled escape into one, and an escape with a name into the surrogate's
// three bytes. Every text has exactly one carried form and every carried form
// one text, so there is no stored value this cannot hold.
const (
	// carryEscape is U+10FFFF, the last code point: a noncharacter, valid in
	// UTF-8 and in JSON, and chosen only for being unlikely -- the scheme
	// does not depend on its being absent.
	carryEscape = '\U0010ffff'
	// carryNames is where the 2,048 surrogates are named: U+E000..U+E7FF.
	carryNames = 0xe000
)

// carryLoneSurrogates rewrites every `\uXXXX` escape that names a lone
// surrogate, and says whether the text went through the rewriting at all.
//
// A surrogate is paired exactly when a high one is followed at once by the
// escape of a low one, which is json.loads' rule: a high surrogate, the same
// high surrogate again and then a low one is a lone high surrogate followed
// by a pair.
func carryLoneSurrogates(raw []byte) ([]byte, bool) {
	if !bytes.Contains(raw, []byte(`\ud`)) && !bytes.Contains(raw, []byte(`\uD`)) {
		// The common case, and the cheap one: no escape names a surrogate of
		// either kind, so there is nothing to carry and nothing to double.
		return raw, false
	}
	escape := string(carryEscape)
	out := make([]byte, 0, len(raw)+16)
	inString := false
	for index := 0; index < len(raw); {
		character := raw[index]
		if !inString {
			inString = character == '"'
			out = append(out, character)
			index++
			continue
		}
		switch {
		case character == '"':
			inString = false
			out = append(out, character)
			index++
		case character != '\\':
			if bytes.HasPrefix(raw[index:], []byte(escape)) {
				// The escape character written as itself: doubled.
				out = append(out, escape+escape...)
				index += len(escape)
				continue
			}
			out = append(out, character)
			index++
		default:
			unit, isUnit := escapedUnit(raw[index:])
			if !isUnit {
				// `\"`, `\\`, `\n` and the rest: two bytes, copied.
				out = append(out, raw[index:min(index+2, len(raw))]...)
				index += 2
				continue
			}
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if low, isLow := escapedUnit(raw[min(index+6, len(raw)):]); isLow &&
					low >= 0xdc00 && low <= 0xdfff {
					if 0x10000+(unit-0xd800)<<10+(low-0xdc00) == carryEscape {
						// The escape character written as a pair: doubled.
						out = append(out, escape+escape...)
					} else {
						out = append(out, raw[index:index+12]...)
					}
					index += 12
					continue
				}
				out = append(out, escape+string(carryNames+(unit-0xd800))...)
				index += 6
			case unit >= 0xdc00 && unit <= 0xdfff:
				out = append(out, escape+string(carryNames+(unit-0xd800))...)
				index += 6
			default:
				out = append(out, raw[index:index+6]...)
				index += 6
			}
		}
	}
	return out, true
}

// escapedUnit reads a `\uXXXX` escape at the start of text.
func escapedUnit(text []byte) (rune, bool) {
	if len(text) < 6 || text[0] != '\\' || text[1] != 'u' {
		return 0, false
	}
	var unit rune
	for _, digit := range text[2:6] {
		if !isHexDigit(digit) {
			return 0, false
		}
		unit = unit<<4 | rune(hexValue(digit))
	}
	return unit, true
}

// uncarry is the other half: a decoded string back to what Python holds.
func uncarry(text string) string {
	if !strings.ContainsRune(text, carryEscape) {
		return text
	}
	var out strings.Builder
	escaped := false
	for _, character := range text {
		switch {
		case escaped && character == carryEscape:
			out.WriteRune(carryEscape)
			escaped = false
		case escaped:
			out.WriteString(surrogateBytes(0xd800 + (character - carryNames)))
			escaped = false
		case character == carryEscape:
			escaped = true
		default:
			out.WriteRune(character)
		}
	}
	return out.String()
}

// errTooDeep is pydantic-core's recursion guard. Serialising a value typed
// `Any` is a walk of the value, and the walk gives up 255 levels down --
// counted from the values of the outermost mapping, and counting a scalar as
// a level like any other. A `json` column holds deeper values than that
// without complaint. It is refused in the same place and so in the same way
// as a lone surrogate: after the route has returned.
var errTooDeep = errors.New("searchconnectors: a value is nested deeper than pydantic serialises")

// maxSerialisedDepth is that guard's limit.
const maxSerialisedDepth = 255

// asPydanticWrites turns a value into what pydantic-core serialises for a
// value typed `Any`, ready for pythonjson.Marshal. `level` is how deep the
// value is: 1 for a value of the outermost mapping.
//
// Four things differ from what Starlette's json.dumps would write, and a
// route with a response_model is written by pydantic:
//
//   - a float is written by ryu (`1e16`, `0.00001`), as
//     pythonjson.PydanticFloat does;
//   - a float that is not finite -- json.loads' answer for `1e400` -- is
//     written as `null` (pydantic's ser_json_inf_nan default), where
//     json.dumps with allow_nan=False would have raised;
//   - a str holding a lone surrogate is refused, whether it is a value or the
//     key of an object (errUnrenderable);
//   - a value more than 255 levels down is refused (errTooDeep).
func asPydanticWrites(value any, level int) (any, error) {
	if level > maxSerialisedDepth {
		return nil, errTooDeep
	}
	switch held := value.(type) {
	case float64:
		if math.IsInf(held, 0) || math.IsNaN(held) {
			return nil, nil
		}
		return pythonjson.PydanticFloat(held), nil
	case string:
		if hasLoneSurrogate(held) {
			return nil, errUnrenderable
		}
		return held, nil
	case []any:
		written := make([]any, 0, len(held))
		for _, item := range held {
			rendered, err := asPydanticWrites(item, level+1)
			if err != nil {
				return nil, err
			}
			written = append(written, rendered)
		}
		return written, nil
	case *pythonjson.Object:
		written := pythonjson.NewObject()
		for _, key := range held.Keys() {
			if hasLoneSurrogate(key) {
				return nil, errUnrenderable
			}
			item, _ := held.Get(key)
			rendered, err := asPydanticWrites(item, level+1)
			if err != nil {
				return nil, err
			}
			written.Set(key, rendered)
		}
		return written, nil
	}
	return value, nil
}

// writeMapping is a field typed `dict[str, Any]` as pydantic-core serialises
// it: the JSON text of the object.
//
// The outermost keys are written as they are. pydantic has a rule of its own
// for an outermost key holding a lone surrogate -- it is declared `str` and
// goes through a lossy conversion, where every level below is `Any` and
// refuses it -- and this function used to mirror that. It no longer can be
// reached: the only mappings written here are a connection's public config,
// whose outermost keys are names from public_config.go, and an MCP server's
// three fields. A key holding a surrogate is never one of those, so there is
// no recorded answer left to hold such a mirror to, and it was removed
// rather than kept unproved.
func writeMapping(mapping *pythonjson.Object) ([]byte, error) {
	out := []byte{'{'}
	for index, key := range mapping.Keys() {
		if index > 0 {
			out = append(out, ',')
		}
		name, err := pythonjson.Marshal(key)
		if err != nil {
			return nil, err
		}
		item, _ := mapping.Get(key)
		written, err := asPydanticWrites(item, 1)
		if err != nil {
			return nil, err
		}
		value, err := pythonjson.Marshal(written)
		if err != nil {
			return nil, err
		}
		out = append(out, name...)
		out = append(out, ':')
		out = append(out, value...)
	}
	return append(out, '}'), nil
}

// fields is a JSON object written one field at a time, in order, with each
// value either rendered here or handed in as text. A response model is a
// fixed list of ASCII field names, so nothing about the names needs escaping.
type fields struct {
	text []byte
}

func (f *fields) raw(name string, value []byte) {
	if len(f.text) == 0 {
		f.text = append(f.text, '{')
	} else {
		f.text = append(f.text, ',')
	}
	f.text = append(f.text, '"')
	f.text = append(f.text, name...)
	f.text = append(f.text, '"', ':')
	f.text = append(f.text, value...)
}

func (f *fields) set(name string, value any) error {
	rendered, err := pythonjson.Marshal(value)
	if err != nil {
		return err
	}
	f.raw(name, rendered)
	return nil
}

func (f *fields) close() []byte {
	if len(f.text) == 0 {
		return []byte("{}")
	}
	return append(f.text, '}')
}

// joinList is a JSON array of already rendered items.
func joinList(items [][]byte) []byte {
	return append(append([]byte{'['}, bytes.Join(items, []byte{','})...), ']')
}
