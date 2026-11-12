package requestbody

import (
	"math"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// OptionalNumber is an `int | None = None` field of a create model: the value
// it carried, and whether that value is one Python holds and the route's next
// query then fails on.
//
// It is not MaybeText's shape, because nothing reads whether such a field was
// *mentioned*: app/routes/folders_routes.py asks only `is not None`, so an
// absent field and an explicit null are the same request there. The integer 0
// is a value and not an absence, which is why this is a pointer and not a zero
// — `parent_id: 0` is "Parent folder not found" on the Python tier rather than
// a folder at the root.
type OptionalNumber struct {
	Value *int64
	// OutOfRange is a number Python reads and a query then fails on. It is
	// held per field rather than on the Model, because which statement fails
	// decides *when* the 500 comes, and a route that checks one field before
	// another has to know which of the two was out of range.
	OutOfRange bool
}

// OptionalNumber reads an `int | None = None` field. Every branch is Number's,
// which was pinned against the running tier one value at a time; the one
// difference is that null is a value of the union here rather than
// NotAnInteger. Asked of POST /api/v1/folders' own parent_id:
//
//	absent, null          -> None
//	0, false              -> 0, which is a parent id and not an absence
//	true, 1, "1", "1_000" -> read
//	1.5                   -> "…got a number with a fractional part"
//	1e19                  -> "Unable to parse input string as an integer, exceeded maximum size"
//	"abc"                 -> "…unable to parse string as an integer"
//	[]                    -> "Input should be a valid integer"
//	2200000000            -> read, and the lookup then fails: a 500, not a 422
func (m *Model) OptionalNumber(name string) OptionalNumber {
	value, present := m.body.Get(name)
	if !present || value == nil {
		return OptionalNumber{}
	}
	return m.optionalNumberValue(name, value)
}

// optionalNumberValue is the coercion on its own, for a value already taken
// out of the body.
func (m *Model) optionalNumberValue(name string, value any) OptionalNumber {
	switch typed := value.(type) {
	case bool:
		read := int64(0)
		if typed {
			read = 1
		}
		return OptionalNumber{Value: &read}
	case pythonjson.RawInt:
		return optionalWholeNumber(string(typed))
	case float64:
		switch {
		case math.IsNaN(typed) || math.IsInf(typed, 0):
			m.reject(NotFinite, name)
		case typed != math.Trunc(typed):
			m.reject(IntegerFractional, name)
		case typed >= math.MaxInt64 || typed <= math.MinInt64:
			m.reject(IntegerTooLarge, name)
		default:
			read := int64(typed)
			return OptionalNumber{Value: &read}
		}
		return OptionalNumber{}
	case string:
		digits, readable := requestnumber.WholeNumber(typed)
		if !readable {
			m.reject(IntegerUnreadable, name)
			return OptionalNumber{}
		}
		return optionalWholeNumber(digits)
	default:
		m.reject(NotAnInteger, name)
		return OptionalNumber{}
	}
}

func optionalWholeNumber(digits string) OptionalNumber {
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return OptionalNumber{OutOfRange: true}
	}
	return OptionalNumber{Value: &value}
}
