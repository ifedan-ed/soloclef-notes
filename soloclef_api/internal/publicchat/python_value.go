package publicchat

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is Python's own behaviour for a value json.loads produced, as far
// as the three routes of this package lean on it. None of those routes asks
// what type a jsonb value is before using it, so each helper below has to
// have an answer for every JSON type, and each answer was taken from the
// Python routes (testdata/publicchat_python.json) rather than reasoned out.
//
// truthy, isPythonInt and formattedInt are the same three rules
// internal/artifacts carries. They are repeated rather than imported because
// they are unexported there; a shared home for them is the right end state.

// errPython is any exception a Python route raised and did not catch: an
// AttributeError for `.get` on something that is not a dict, a TypeError for
// iterating a number, a ValueError from dict(). The routes of this package
// catch nothing, so each is the same sanitised 500 with no CORS headers, and
// which exception it was is only of interest to the log.
type errPython struct{ what string }

func (e errPython) Error() string { return "publicchat: Python raises here: " + e.what }

func raised(what string) error { return errPython{what: what} }

// maxIntegerDigits is CPython's limit on the digits of an integer it will
// read from text (sys.int_info.default_max_str_digits), and pydantic-core's
// limit on the length of one it will parse. The two are the same number and
// are not the same rule:
//
//   - json.loads refuses a column holding an integer of more digits than
//     this, so the row cannot be loaded at all and every route answers 500
//     for it, whether or not it reads that part of the value (decodeColumn);
//   - pydantic refuses a string longer than this as an integer, by a rule of
//     its own about which part of the string is counted (integerFromText).
const maxIntegerDigits = 4300

var errUnloadable = errors.New("publicchat: the column holds an integer json.loads refuses to read")

// decodeColumn is what SQLAlchemy hands a route for a jsonb column: None for
// a SQL NULL, and json.loads of the text otherwise -- which is also None for
// the JSON scalar null, so the two are one value from here on.
//
// It fails where json.loads fails. Postgres has already checked the text is
// JSON, so the one failure left is an integer past maxIntegerDigits.
func decodeColumn(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	value, err := pythonjson.Decode(raw)
	if err != nil {
		return nil, err
	}
	if holdsUnreadableInteger(value) {
		return nil, errUnloadable
	}
	return value, nil
}

func holdsUnreadableInteger(value any) bool {
	switch held := value.(type) {
	case pythonjson.RawInt:
		return significantDigits(string(held)) > maxIntegerDigits
	case []any:
		for _, item := range held {
			if holdsUnreadableInteger(item) {
				return true
			}
		}
	case *pythonjson.Object:
		for _, key := range held.Keys() {
			if holdsUnreadableInteger(held.GetOrNil(key)) {
				return true
			}
		}
	}
	return false
}

// significantDigits counts an integer's digits as CPython counts them against
// its limit: without the sign and without leading zeros.
func significantDigits(digits string) int {
	return len(strings.TrimLeft(strings.TrimLeft(digits, "+-"), "0"))
}

// truthy is Python's bool() of a value json.loads produced: None, False, zero
// in either number form, and the empty string, list and object are false, and
// everything else -- a blank string, a list holding a zero -- is true.
func truthy(value any) bool {
	switch held := value.(type) {
	case nil:
		return false
	case bool:
		return held
	case string:
		return held != ""
	case pythonjson.RawInt:
		return significantDigits(string(held)) > 0
	case float64:
		return held != 0
	case []any:
		return len(held) > 0
	case *pythonjson.Object:
		return held.Len() > 0
	}
	// pythonjson.Decode produces nothing else.
	return true
}

// pythonInt is the integer a value is when `isinstance(value, int)` holds,
// and false when it does not.
//
// It holds for an integer and -- because bool is a subclass of int in Python
// -- for true and false, which are 1 and 0. A float is not an int even when
// it is whole: json.loads reads 7.0 as a float and the test refuses it.
func pythonInt(value any) (*big.Int, bool) {
	switch held := value.(type) {
	case pythonjson.RawInt:
		return wholeNumber(string(held))
	case bool:
		if held {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	}
	return nil, false
}

// wholeNumber reads decimal digits of any width, which is what a Python int
// is. The text is always one this tier produced or Postgres printed, so a
// failure is a mistake in this package rather than an input.
func wholeNumber(digits string) (*big.Int, bool) {
	return new(big.Int).SetString(digits, 10)
}

// formattedInt is how an f-string writes a value that passed
// `isinstance(value, int)`: an integer as its digits, and a bool as the word
// Python prints for it, capital first -- so a slide numbered `true` gets an
// audio URL ending ".../slides/True/audio".
func formattedInt(value any) string {
	switch held := value.(type) {
	case pythonjson.RawInt:
		number, _ := wholeNumber(string(held))
		return number.String()
	case bool:
		if held {
			return "True"
		}
		return "False"
	}
	// Unreachable: every caller asks pythonInt first.
	return ""
}

// number is a JSON number as Python compares one: an exact integer or a
// float. Python's `==` between an int and a float is exact -- it does not
// round the int to a float first -- so 9007199254740993 is not equal to
// 9007199254740992.0, and 10**20 is equal to 1e20.
type number struct {
	whole    *big.Int
	fraction float64
	isFloat  bool
}

// numberOf is the number a JSON value is to Python's `==`: an integer, a
// float, or a bool as 1 or 0. Everything else is not a number.
func numberOf(value any) (number, bool) {
	if whole, isInt := pythonInt(value); isInt {
		return number{whole: whole}, true
	}
	if float, isFloat := value.(float64); isFloat {
		return number{fraction: float, isFloat: true}, true
	}
	return number{}, false
}

func (n number) equals(other number) bool {
	switch {
	case n.isFloat && other.isFloat:
		// 0.0 == -0.0, and an infinity equals itself; NaN cannot come out
		// of jsonb.
		return n.fraction == other.fraction
	case !n.isFloat && !other.isFloat:
		return n.whole.Cmp(other.whole) == 0
	case n.isFloat:
		return floatEqualsWhole(n.fraction, other.whole)
	default:
		return floatEqualsWhole(other.fraction, n.whole)
	}
}

// floatEqualsWhole is `float == int`, exactly: true only when the float has
// no fractional part and its exact value is that integer.
func floatEqualsWhole(float float64, whole *big.Int) bool {
	if math.IsInf(float, 0) || math.IsNaN(float) || float != math.Trunc(float) {
		return false
	}
	// A float64 that is whole is an integer exactly, however large.
	exact, _ := new(big.Float).SetFloat64(float).Int(nil)
	return exact.Cmp(whole) == 0
}

// equalsInt is `value == n` for an integer n, which is what `x in a_list`
// asks of each element and what `podcast.get("original_id") == podcast_id`
// asks: true for the same integer, for a float that is exactly it, and for
// true against 1 and false against 0. A string never equals an integer, so
// "31" is not 31.
func equalsInt(value any, n *big.Int) bool {
	held, isNumber := numberOf(value)
	return isNumber && held.equals(number{whole: n})
}

// sameKey is whether two values are one key of a Python dict: equal, with
// equal hashes. Among the values json.loads produces that is: the same
// string; None and None; or two numbers that compare equal, where a bool is
// the number it is. So 1, 1.0 and True are one key, and "1" is another.
func sameKey(left, right any) bool {
	if leftText, isText := left.(string); isText {
		rightText, alsoText := right.(string)
		return alsoText && leftText == rightText
	}
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftNumber, leftIsNumber := numberOf(left)
	rightNumber, rightIsNumber := numberOf(right)
	return leftIsNumber && rightIsNumber && leftNumber.equals(rightNumber)
}

// hashable is whether Python can use a value as a dict key or put it in a
// set: everything json.loads produces except a list and a dict.
func hashable(value any) bool {
	switch value.(type) {
	case []any, *pythonjson.Object:
		return false
	}
	return true
}

// containsInt is `n in container` for the `artifact_ids` allowlist, where
// container is `snapshot_data.get("artifact_ids") or []`.
//
// The allowlist is meant to be a list of integers and the routes do not
// check, so:
//
//	anything falsy      -> `or []` makes it an empty list: not in it
//	a list              -> true if any element equals n (equalsInt)
//	a non-empty object  -> `in` asks about its keys, which are strings: no
//	a non-empty string  -> TypeError; `in` on a str wants a str on the left
//	a number, or true   -> TypeError; not iterable
func containsInt(container any, n *big.Int) (bool, error) {
	if !truthy(container) {
		return false, nil
	}
	switch held := container.(type) {
	case []any:
		for _, item := range held {
			if equalsInt(item, n) {
				return true, nil
			}
		}
		return false, nil
	case *pythonjson.Object:
		return false, nil
	case string:
		return false, raised("`int in str` is a TypeError")
	}
	return false, raised("`in` on a value that is not iterable")
}

// dict is a Python dict whose keys need not be strings.
//
// A JSON object's keys always are, and pythonjson.Object is the dict for
// that. This exists for the one place a route *builds* a dict out of
// something else: the video route calls dict() on every stored slide, and
// dict() of a list of pairs is a dict keyed by whatever each pair began with
// -- a number, a bool, None. Python then keeps the first spelling of a key
// and the last value, which is what Set does.
type dict struct {
	keys   []any
	values []any
}

func (d *dict) index(key any) int {
	for position, held := range d.keys {
		if sameKey(held, key) {
			return position
		}
	}
	return -1
}

// Set is `d[key] = value`: a new key goes at the end, and an existing one
// keeps its place *and its own spelling* -- {True: "a"} assigned at 1 is
// still keyed True.
func (d *dict) Set(key, value any) {
	if position := d.index(key); position >= 0 {
		d.values[position] = value
		return
	}
	d.keys = append(d.keys, key)
	d.values = append(d.values, value)
}

// Get is `d.get(name)` for a string name: None when it is not there.
func (d *dict) Get(name string) any {
	if position := d.index(name); position >= 0 {
		return d.values[position]
	}
	return nil
}

// Pop is `d.pop(name, None)`.
func (d *dict) Pop(name string) any {
	position := d.index(name)
	if position < 0 {
		return nil
	}
	value := d.values[position]
	d.keys = append(d.keys[:position], d.keys[position+1:]...)
	d.values = append(d.values[:position], d.values[position+1:]...)
	return value
}

// dictOf is Python's `dict(value)` for a value json.loads produced.
//
//	an object           -> a copy of it
//	"" and []           -> an empty dict: nothing to iterate
//	a list              -> each element must be a pair -- see pairOf
//	a non-empty string  -> ValueError: each character is a sequence of one
//	None, a number, a bool -> TypeError: not iterable
func dictOf(value any) (*dict, error) {
	built := &dict{}
	switch held := value.(type) {
	case *pythonjson.Object:
		for _, key := range held.Keys() {
			built.Set(key, held.GetOrNil(key))
		}
		return built, nil
	case string:
		if held == "" {
			return built, nil
		}
		return nil, raised("dict() of a non-empty string")
	case []any:
		for _, element := range held {
			key, item, err := pairOf(element)
			if err != nil {
				return nil, err
			}
			built.Set(key, item)
		}
		return built, nil
	}
	return nil, raised("dict() of a value that is not iterable")
}

// pairOf is what dict() makes of one element of the sequence it was given:
// anything that iterates to exactly two things is a key and a value.
//
//	[k, v]              -> k: v, and k must be hashable
//	a two-character str -> the first character: the second ("ab" is a: b).
//	                       Characters are code points, as a Python str's are.
//	a two-key object    -> its first key: its second key; the values are not
//	                       looked at, because iterating a dict yields keys
//	anything else       -> ValueError for the wrong length, TypeError for a
//	                       value that is not a sequence or a key that is a
//	                       list or an object
func pairOf(element any) (key, value any, err error) {
	switch held := element.(type) {
	case []any:
		if len(held) != 2 {
			return nil, nil, raised("dict() element is not a pair")
		}
		if !hashable(held[0]) {
			return nil, nil, raised("dict() key is unhashable")
		}
		return held[0], held[1], nil
	case string:
		if utf8.RuneCountInString(held) != 2 {
			return nil, nil, raised("dict() element is a string that is not two characters")
		}
		_, width := utf8.DecodeRuneInString(held)
		return held[:width], held[width:], nil
	case *pythonjson.Object:
		if held.Len() != 2 {
			return nil, nil, raised("dict() element is an object without exactly two keys")
		}
		return held.Keys()[0], held.Keys()[1], nil
	}
	return nil, nil, raised("dict() element is not a sequence")
}
