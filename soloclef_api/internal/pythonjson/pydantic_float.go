package pythonjson

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// PydanticFloat is a float rendered as pydantic renders one, which is not how
// Starlette renders one.
//
// A route with no response_model hands its body to json.dumps, so a float is
// written by Python's repr: 1e16 is "1e+16" and 1e-5 is "1e-05"
// (formatFloat, in encode.go). A route **with** a response_model is serialised
// by pydantic-core instead, which is Rust and writes through ryu: the same two
// floats are "1e16" and "0.00001". Both appear in this deployment —
// GET /api/v1/logs/workspaces/{id}/summary has no response_model and
// GET /api/v1/logs/{log_id} has one — and the same `log_metadata` column
// reaches both.
//
// The rule below is ryu's "pretty" formatting, pinned against the running
// Python tier one float at a time through POST /api/v1/logs' own metadata:
//
//	1e15  -> 1000000000000000.0    1e16   -> 1e16
//	1e-4  -> 0.0001                1e-5   -> 0.00001     1e-6 -> 1e-6
//	5e-324 -> 5e-324               -0.0   -> -0.0        100.0 -> 100.0
//	12345678901234567.0 -> 1.2345678901234568e16
//
// Written as: let the shortest round-tripping decimal be `digits` (k of them)
// with the decimal point `pointAt` places from its left, so the value is
// 0.digits × 10^pointAt. Then decimal notation is used while
// -5 < pointAt <= 16, and scientific notation outside it — with no "+" and no
// zero padding on the exponent, which is where repr differs most visibly.
type PydanticFloat float64

// PydanticValues deep-copies a decoded value, replacing every float with the
// pydantic rendering. Everything else is left exactly as it is, so an integer
// keeps its text and an object keeps its key order.
func PydanticValues(value any) any {
	switch typed := value.(type) {
	case float64:
		return PydanticFloat(typed)
	case []any:
		converted := make([]any, 0, len(typed))
		for _, item := range typed {
			converted = append(converted, PydanticValues(item))
		}
		return converted
	case *Object:
		converted := NewObject()
		for _, key := range typed.keys {
			converted.Set(key, PydanticValues(typed.values[key]))
		}
		return converted
	default:
		return value
	}
}

// formatPydanticFloat is the rule in the type's comment.
func formatPydanticFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		// pydantic-core refuses a non-finite float before it is ever
		// serialised, so this tier must not invent one either.
		return "", fmt.Errorf("pythonjson: %v is out of range for JSON", f)
	}

	shortest := strconv.FormatFloat(f, 'e', -1, 64)
	sign := ""
	if strings.HasPrefix(shortest, "-") {
		sign = "-"
		shortest = shortest[1:]
	}
	mantissa, exponentText, _ := strings.Cut(shortest, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return "", fmt.Errorf("pythonjson: %q is not a float: %w", shortest, err)
	}
	// FormatFloat('e') writes one digit before the point, so the value is
	// 0.digits × 10^(exponent+1).
	pointAt := exponent + 1

	if pointAt > -5 && pointAt <= 16 {
		return sign + decimalNotation(digits, pointAt), nil
	}
	return sign + scientificNotation(digits, pointAt), nil
}

// decimalNotation writes the digits with the point at pointAt, which may be at
// or past their end (zeros are added and ".0" closes it) or at or before their
// start (a "0." and leading zeros).
func decimalNotation(digits string, pointAt int) string {
	switch {
	case pointAt <= 0:
		return "0." + strings.Repeat("0", -pointAt) + digits
	case pointAt >= len(digits):
		return digits + strings.Repeat("0", pointAt-len(digits)) + ".0"
	default:
		return digits[:pointAt] + "." + digits[pointAt:]
	}
}

// scientificNotation writes one digit, the rest after a point when there is a
// rest, and the exponent with a minus sign only — no "+", no zero padding.
func scientificNotation(digits string, pointAt int) string {
	text := digits[:1]
	if len(digits) > 1 {
		text += "." + digits[1:]
	}
	return text + "e" + strconv.Itoa(pointAt-1)
}
