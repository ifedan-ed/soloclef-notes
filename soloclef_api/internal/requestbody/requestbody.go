// Package requestbody reads a request body the way a pydantic model reads it,
// and reports what it cannot read as the Python tier's own 422 rather than in
// words of this tier's own.
//
// The first write handed over takes a JSON body, which is a validation surface
// no read route had: a field that is missing, one of the wrong type, one too
// long, a list whose third item is a number. FastAPI reports all of them at
// once, in the model's field order, with the location path pydantic gives each
// one — and app.py's _validation_error_handler turns that into the envelope
// internal/errorenvelope writes. So the failures are produced here and written
// there, exactly as a path parameter's are (internal/requestnumber).
//
// What this package deliberately does not do is position a *decode* failure.
// pydantic-core parses the body with its own JSON parser, whose byte offset is
// the location it reports — "1" for `{`, "3" for three spaces, "10" for a tab
// inside a string — and whose idea of valid JSON is not encoding/json's: it
// takes NaN and Infinity, and a body that is not UTF-8 never reaches it at all
// and is answered by a different handler with a 400. Mirroring that parser's
// offsets is a parser, not a mirror, so a body this tier cannot decode is
// reported as ErrUndecodable and the route hands the request to the Python
// tier, which positions it itself. That is safe for a write because such a body
// never reaches the Python route's own code either: it is refused before any
// statement runs, so nothing is written twice.
package requestbody

import "errors"

// ErrUndecodable is a body this tier will not answer for: not UTF-8, or not
// JSON that encoding/json reads. The Python tier's parser decides where the
// fault is.
var ErrUndecodable = errors.New("requestbody: the body's decode failure belongs to the Python tier")

// The messages pydantic gives, as the Python tier was asked for them one case
// at a time.
const (
	// FieldRequired is reported for a field that is absent, and for a body that
	// is absent or JSON null, where the missing field is the model itself.
	FieldRequired = "Field required"
	// NotAnObject is the model-level message for a body that decoded to
	// something other than an object, and for one whose content type FastAPI
	// does not read as JSON at all.
	NotAnObject = "Input should be a valid dictionary or object to extract fields from"
	// NotAString is reported for any field typed str whose value is not one,
	// including null: pydantic does not coerce a number to a string.
	NotAString = "Input should be a valid string"
	// NotAList is reported for a field typed list whose value is not one.
	NotAList = "Input should be a valid list"
	// NotABoolean is reported for a value of a type no bool can be read from.
	NotABoolean = "Input should be a valid boolean"
	// BooleanUnreadable is reported for a value of the right kind that still
	// names no truth: the integer 2, the word "maybe", the empty string.
	BooleanUnreadable = "Input should be a valid boolean, unable to interpret input"
	// NotAnInteger is reported for a value of a type no int can be read from,
	// null among them.
	NotAnInteger = "Input should be a valid integer"
	// IntegerUnreadable is reported for a string that names no integer.
	IntegerUnreadable = "Input should be a valid integer, unable to parse string as an integer"
	// IntegerFractional is reported for a JSON number with a fraction.
	IntegerFractional = "Input should be a valid integer, got a number with a fractional part"
	// IntegerTooLarge is reported for a float no int64 can hold, which — unlike
	// an integer of the same size — is a refusal rather than a 500.
	IntegerTooLarge = "Unable to parse input string as an integer, exceeded maximum size"
	// NotFinite is reported for a float that is NaN or an infinity.
	NotFinite = "Input should be a finite number"
	// NotADictionary is reported for a *field* typed dict whose value is not
	// one. It is not NotAnObject, which is the model-level message.
	NotADictionary = "Input should be a valid dictionary"
)

// BodyRoot is the first element of every failure's location path for a body
// field. app.py drops it from the message it builds and keeps it in
// error.fields, which is why it is part of the location rather than added
// there.
const BodyRoot = "body"
