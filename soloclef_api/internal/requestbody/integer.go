package requestbody

import (
	"math"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Number reads a required `int` field with pydantic's lax coercion, which is
// not a type assertion and not the same rule as a query parameter's — though it
// shares the string branch with it (internal/requestnumber).
//
// Each branch was asked of the running Python tier through
// POST /api/v1/logs' own workspace_id:
//
//	1, "1", " 1 ", "1_000", "1.0", 1.0, 1e18, true, false -> read
//	1.5                  -> "…got a number with a fractional part"
//	1e19                 -> "Unable to parse input string as an integer, exceeded maximum size"
//	1e400                -> "Input should be a finite number"
//	"abc", ""            -> "…unable to parse string as an integer"
//	null, [], {}         -> "Input should be a valid integer"
//	99999999999999999999 -> read, and the query then fails: a 500, not a 422
//
// A value Python holds and then fails on is recorded as out of range rather
// than as a validation failure, because the Python route answers it after the
// body has validated.
func (m *Model) Number(name string) int64 {
	value, present := m.body.Get(name)
	if !present {
		m.reject(FieldRequired, name)
		return 0
	}

	switch typed := value.(type) {
	case bool:
		// pydantic reads a JSON boolean as an int in lax mode, so a workspace
		// id of `true` is workspace 1. Checked rather than assumed.
		if typed {
			return 1
		}
		return 0
	case pythonjson.RawInt:
		return m.wholeNumber(name, string(typed))
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			m.reject(NotFinite, name)
			return 0
		}
		if typed != math.Trunc(typed) {
			m.reject(IntegerFractional, name)
			return 0
		}
		if typed >= math.MaxInt64 || typed <= math.MinInt64 {
			m.reject(IntegerTooLarge, name)
			return 0
		}
		return int64(typed)
	case string:
		digits, readable := requestnumber.WholeNumber(typed)
		if !readable {
			m.reject(IntegerUnreadable, name)
			return 0
		}
		return m.wholeNumber(name, digits)
	default:
		m.reject(NotAnInteger, name)
		return 0
	}
}

// wholeNumber holds the one case that is not a refusal: Python reads an integer
// of any width, so a value too wide for a column is the query's failure and the
// route's sanitised 500.
func (m *Model) wholeNumber(name, digits string) int64 {
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		m.outOfRange = true
		return 0
	}
	return value
}
