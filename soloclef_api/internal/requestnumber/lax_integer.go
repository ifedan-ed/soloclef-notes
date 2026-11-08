package requestnumber

import (
	"strings"
	"unicode"
)

// WholeNumber is pydantic-core's `str_as_int`, which is what a parameter typed
// `int` is read with: the digits it names, or false when it names none.
//
// It is far more forgiving than it looks, and the cases below are not
// decoration — each was asked of the running Python tier through a route's own
// `hours` parameter, whose answer echoes the value it parsed:
//
//	"1_000"      -> 1000   single underscores between digits, Python's own
//	                       integer literal separator
//	" 1 ", "\t1" -> 1      Unicode whitespace at either end is trimmed
//	"\u00a01"    -> 1      including a non-breaking space, U+2028 and U+3000
//	"0001", "+0" -> 1, 0   leading zeros and a leading plus
//	"1.0", "1.00" -> 1     a decimal form whose fraction is all zeros
//	"1_000.0"    -> 1000   underscores in that form's integer part too
//	"1.5"        -> refused
//	"1e3", "1.0e3", "0x10", "0b1" -> refused; no exponent and no radix prefix
//	"_1", "1_", "1__0", "1.0_0", ".0", "1." -> refused
//	"٢", "１"     -> refused; ASCII digits only
//
// This mirror exists because it was got wrong: until it was written this
// package accepted `^[+-]?\d+$` and nothing else, so `GET
// /api/v1/workspaces/1.0/my-access` was a 200 on the Python tier and a 422
// here, on a route that had already been handed over. Do not simplify it back.
func WholeNumber(text string) (string, bool) {
	trimmed := strings.TrimFunc(text, unicode.IsSpace)
	integer, fraction, hasFraction := strings.Cut(trimmed, ".")
	if hasFraction && !allZeros(fraction) {
		return "", false
	}
	return readIntegerLiteral(integer)
}

// readIntegerLiteral accepts an optional sign and then ASCII digits separated
// by at most one underscore each, and returns the digits with the underscores
// removed. An underscore that leads, trails or doubles is refused, which is
// Python's own rule for an integer literal.
func readIntegerLiteral(text string) (string, bool) {
	digits := make([]byte, 0, len(text))
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		if text[0] == '-' {
			digits = append(digits, '-')
		}
		text = text[1:]
	}
	if text == "" {
		return "", false
	}

	previousWasUnderscore := false
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case character >= '0' && character <= '9':
			digits = append(digits, character)
			previousWasUnderscore = false
		case character == '_':
			// Never first, never last, never two in a row.
			if index == 0 || index == len(text)-1 || previousWasUnderscore {
				return "", false
			}
			previousWasUnderscore = true
		default:
			return "", false
		}
	}
	return string(digits), true
}

// allZeros is the fraction a decimal form may carry: one or more ASCII zeros
// and nothing else, so "1.0" is one and "1." and "1.0_0" are not.
func allZeros(fraction string) bool {
	if fraction == "" {
		return false
	}
	for index := 0; index < len(fraction); index++ {
		if fraction[index] != '0' {
			return false
		}
	}
	return true
}
