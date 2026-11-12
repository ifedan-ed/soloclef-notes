package publicchat

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The page route declares `response_model=PublicChatResponse`, so what
// get_public_chat returns is not what is sent. FastAPI validates the dict
// against the model first -- in pydantic's *lax* mode, which coerces -- and
// pydantic-core then serialises the model. Three consequences reach the wire
// and this file is the mirror of each:
//
//   - a field the dict has and the model does not is dropped, and the fields
//     are written in the model's order, not the dict's;
//   - a value of a neighbouring type is coerced rather than refused: "31" and
//     31.0 and true are integers, and a number or a numeric string is a
//     datetime;
//   - a value the model refuses is a ResponseValidationError, which nothing
//     catches: the sanitised 500, with no CORS headers.
//
// So every reader below answers "the value as the model holds it, or
// refused", and a refusal anywhere is the same 500. Which field it was is
// only for the log.

// errRefused is a value the response model does not accept.
type errRefused struct{ field string }

func (e errRefused) Error() string {
	return "publicchat: the response model refuses " + e.field
}

func refused(field string) error { return errRefused{field: field} }

// modelText is a `str` field. Lax mode does not make a string out of a
// number: only a string is one.
func modelText(value any, field string) (string, error) {
	text, isText := value.(string)
	if !isText {
		return "", refused(field)
	}
	return text, nil
}

// modelInt is an `int` field in lax mode, as the integer's digits.
//
//	an integer        -> itself, at any width
//	true, false       -> 1, 0
//	a whole float     -> that integer, but only strictly inside ±2**63: at
//	                     2**63 exactly, or beyond, it is refused
//	a float with a fractional part, or not finite -> refused
//	a string          -> pydantic-core's str_as_int, as a path parameter is
//	                     read (integerFromText): " 3_1.0 " is 31
//	anything else     -> refused
//
// The digits rather than an int64, because the model holds a Python int:
// a file size of 92233720368547758070 is served as that.
func modelInt(value any, field string) (pythonjson.RawInt, error) {
	switch held := value.(type) {
	case pythonjson.RawInt, bool:
		whole, _ := pythonInt(held)
		return pythonjson.RawInt(whole.String()), nil
	case float64:
		const limit = 9223372036854775808.0 // 2**63
		if math.IsNaN(held) || math.IsInf(held, 0) || held != math.Trunc(held) ||
			held <= -limit || held >= limit {
			return "", refused(field)
		}
		whole, _ := new(big.Float).SetFloat64(held).Int(nil)
		return pythonjson.RawInt(whole.String()), nil
	case string:
		whole, refusal := integerFromText(held)
		if refusal != "" {
			return "", refused(field)
		}
		return pythonjson.RawInt(whole.String()), nil
	}
	return "", refused(field)
}

// modelMoment is a `datetime` field in lax mode, already rendered as
// pydantic-core writes one.
//
// Reading is internal/requestbody.ReadMoment, the mirror of pydantic's
// datetime coercion that the invite routes' bodies are read with: ISO 8601
// text, or a number or numeric string taken as a unix timestamp. Everything
// that reader documents applies here, including its one recorded divergence
// (a numeric *string* with a fraction and thirteen or more whole digits),
// which no snapshot this app writes can hold -- `snapshot_at` and a
// message's `created_at` are both `datetime.isoformat()`.
//
// Writing is not pythonjson.PydanticDateTime, which converts to UTC because
// every datetime it is given came from a timestamptz column. A datetime
// pydantic *parsed* keeps what it was written with: an offset stays that
// offset, and one written with no offset is naive and is written back with
// none. Only a zero offset is spelled "Z".
func modelMoment(value any, field string) (string, error) {
	moment, naive, failure := requestbody.ReadMoment(value)
	if failure != "" {
		return "", refused(field)
	}
	return renderMoment(moment, naive), nil
}

// renderMoment writes a parsed datetime as pydantic-core does: the wall-clock
// time it was written with, six fraction digits when there is a fraction and
// none when there is not, and then nothing (naive), "Z" (a zero offset) or
// ±HH:MM.
func renderMoment(moment time.Time, naive bool) string {
	text := moment.Format("2006-01-02T15:04:05")
	if microseconds := moment.Nanosecond() / 1000; microseconds != 0 {
		text += fmt.Sprintf(".%06d", microseconds)
	}
	if naive {
		return text
	}
	_, offset := moment.Zone()
	if offset == 0 {
		return text + "Z"
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return text + fmt.Sprintf("%s%02d:%02d", sign, offset/3600, offset%3600/60)
}

// modelAny is a field typed `Any`, as pydantic-core serialises whatever it
// holds: validation accepts everything, and the value is written through.
//
// Two renderings differ from Starlette's, which is why this is not
// renderUnmodelled. A float is written by ryu rather than by Python's repr
// (pythonjson.PydanticFloat: 1e16, not 1e+16). And a float that is not
// finite -- jsonb holds 1e999 exactly, and json.loads reads it as inf -- is
// written `null`, where json.dumps on the other two routes raises: pydantic's
// default for `ser_json_inf_nan` is "null".
//
// No key is dropped: jsonable_encoder never sees a modelled response, so a
// "_sa…" key that the other two routes lose is kept here.
func modelAny(value any) any {
	switch held := value.(type) {
	case float64:
		if math.IsInf(held, 0) || math.IsNaN(held) {
			return nil
		}
		return pythonjson.PydanticFloat(held)
	case []any:
		written := make([]any, 0, len(held))
		for _, item := range held {
			written = append(written, modelAny(item))
		}
		return written
	case *pythonjson.Object:
		written := pythonjson.NewObject()
		for _, key := range held.Keys() {
			written.Set(key, modelAny(held.GetOrNil(key)))
		}
		return written
	}
	return value
}

// quoted is urllib.parse.quote(text, safe=""): every byte of the UTF-8 text
// percent-encoded in upper-case hex, except the letters, the digits and the
// four marks `_.-~`.
//
// It is not url.PathEscape, which leaves `$&+,:;=@` alone, nor
// url.QueryEscape, which writes a space as "+": the answer has to be the
// bytes Python wrote, because the web app uses the URL as it is given.
func quoted(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case character >= 'A' && character <= 'Z',
			character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '_', character == '.', character == '-', character == '~':
			out.WriteByte(character)
		default:
			fmt.Fprintf(&out, "%%%02X", character)
		}
	}
	return out.String()
}
