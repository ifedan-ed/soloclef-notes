package documents

import (
	"strconv"
	"strings"
	"unicode"
)

// pythonMostDigits is sys.int_info.default_max_str_digits: CPython refuses to
// read a decimal string of more digits than this as an integer, with a
// ValueError like any other bad integer. The Python tier runs with the
// default.
const pythonMostDigits = 4300

// pythonInt is Python's built-in `int(text)` for a string that `str.strip()`
// has already been applied to, which is how get_documents_status reads each
// id of its comma-separated list.
//
// It is NOT the reader the other parameters of this tier use
// (internal/requestnumber.WholeNumber). That one is pydantic's, which FastAPI
// applies to a parameter typed `int`; this is the language's own, which the
// route calls itself on pieces of a parameter typed `str`. They disagree, and
// each of these is how the language itself reads the text:
//
//	"61.0"   pydantic reads 61         int() refuses
//	"٦١"     pydantic refuses          int() reads 61: any Unicode decimal digit
//	"６１"   pydantic refuses          int() reads 61
//
// What they share: an optional sign, single underscores between digits and
// nowhere else, leading zeros, and no radix prefix or exponent.
//
// Three more rules that a request can reach:
//
//   - there is no width. Twenty digits are an integer, and what happens next
//     is the statement's business, so the sign of a number past int64 is kept
//     (number.beyond);
//   - past 4300 digits it is refused. The count is of digits: a sign and the
//     underscores do not count and leading zeros do;
//   - nothing may stand between the sign and the first digit, and no space
//     may stand anywhere inside, because the only whitespace int() forgives
//     is at the two ends and strip() has already taken that.
//
// A decimal digit is a character of Unicode's category Nd. Python reads its
// value from its own Unicode tables and Go has no such table, so the value is
// worked out from a rule Unicode guarantees instead: decimal digits are
// encoded in unbroken runs of ten, zero first. The two languages' tables are
// of different Unicode versions, so a digit added to the standard between
// them would be read by one tier and not the other; none of the scripts in
// use is that new.
func pythonInt(text string) (number, bool) {
	negative := false
	switch {
	case strings.HasPrefix(text, "-"):
		negative = true
		text = text[1:]
	case strings.HasPrefix(text, "+"):
		text = text[1:]
	}

	digits := make([]byte, 0, len(text)+1)
	if negative {
		digits = append(digits, '-')
	}
	count := 0
	previousWasUnderscore := true // so that a leading underscore is refused
	for _, character := range text {
		if character == '_' {
			if previousWasUnderscore {
				return number{}, false
			}
			previousWasUnderscore = true
			continue
		}
		value, isDigit := decimalValue(character)
		if !isDigit {
			return number{}, false
		}
		digits = append(digits, '0'+value)
		count++
		previousWasUnderscore = false
	}
	// No digits at all, or a trailing underscore.
	if count == 0 || previousWasUnderscore {
		return number{}, false
	}
	if count > pythonMostDigits {
		return number{}, false
	}

	value, err := strconv.ParseInt(string(digits), 10, 64)
	if err != nil {
		// Only a range error is reachable: the syntax was just checked.
		if negative {
			return number{beyond: -1}, true
		}
		return number{beyond: 1}, true
	}
	return number{value: value}, true
}

// decimalValue is the digit a character of category Nd stands for.
func decimalValue(character rune) (byte, bool) {
	if character >= '0' && character <= '9' {
		return byte(character - '0'), true
	}
	if !unicode.Is(unicode.Nd, character) {
		return 0, false
	}
	// Walk back to the start of the run this digit is in. Runs are whole
	// tens -- some scripts have several tens back to back -- so the distance
	// from the start, modulo ten, is the value.
	start := character
	for unicode.Is(unicode.Nd, start-1) {
		start--
	}
	return byte((character - start) % 10), true
}
