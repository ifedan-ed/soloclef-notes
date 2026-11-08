package requestbody

import (
	"math"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// trueWords and falseWords are pydantic's lax-bool vocabulary, matched without
// regard to case and without trimming: " true " is refused and "yEs" is
// accepted. They look like a long way to read a boolean and they are what the
// Python tier does, token by token.
var (
	trueWords  = map[string]bool{"1": true, "on": true, "t": true, "true": true, "y": true, "yes": true}
	falseWords = map[string]bool{"0": true, "off": true, "f": true, "false": true, "n": true, "no": true}
)

// Truth reads a `bool` field with pydantic's lax coercion, which is why this is
// not a type assertion.
//
// A JSON boolean is itself. A number is read as a boolean only when it is
// exactly 0 or 1 — including the float 1.0 and the float -0.0 — and any other
// whole number is "unable to interpret input" while a fractional one is not a
// boolean at all. A string is read from the vocabulary above. Everything else,
// null and a list and an object among them, is not a boolean.
//
// The two messages are not interchangeable: the Python tier answers the integer
// 2 and the word "maybe" with one and the float 0.5 and null with the other.
func (m *Model) Truth(name string, fallback bool) bool {
	value, present := m.body.Get(name)
	if !present {
		return fallback
	}
	truth, readable := m.readTruth(name, value)
	if !readable {
		return fallback
	}
	return truth
}

// readTruth is the coercion itself, shared with the optional form a
// partial-update model reads (optional.go). A value it cannot read has already
// been rejected when it reports false.
func (m *Model) readTruth(name string, value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case pythonjson.RawInt:
		number, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			// An integer too wide for int64 is still an integer, and still
			// neither 0 nor 1.
			m.reject(BooleanUnreadable, name)
			return false, false
		}
		return m.truthOfWholeNumber(name, number)
	case float64:
		if typed != math.Trunc(typed) || math.IsInf(typed, 0) || math.IsNaN(typed) {
			m.reject(NotABoolean, name)
			return false, false
		}
		return m.truthOfWholeNumber(name, int64(typed))
	case string:
		word := strings.ToLower(typed)
		if trueWords[word] {
			return true, true
		}
		if falseWords[word] {
			return false, true
		}
		m.reject(BooleanUnreadable, name)
		return false, false
	default:
		m.reject(NotABoolean, name)
		return false, false
	}
}

func (m *Model) truthOfWholeNumber(name string, number int64) (bool, bool) {
	switch number {
	case 0:
		return false, true
	case 1:
		return true, true
	default:
		m.reject(BooleanUnreadable, name)
		return false, false
	}
}
