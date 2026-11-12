package requestbody

import (
	"math"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The refusals pydantic words for a `datetime` it cannot read. Every one of
// them was taken off the running Python version, not from the library's
// documentation: testdata/moment_python.json holds what it answered for 136
// shapes of input, and moment_test.go replays them.
//
// pydantic reports three error *types* here -- datetime_type,
// datetime_parsing and datetime_from_date_parsing -- but only the message
// reaches a client through this tier's envelope (internal/errorenvelope), so
// only the messages are mirrored.
const (
	// NotADateTime is for a value of a type no datetime can be read from: a
	// boolean, a list, an object. Notably *not* a number, which is a unix
	// timestamp, and not a numeric string, which is one too.
	NotADateTime = "Input should be a valid datetime"

	// The date parser's own complaints. pydantic reaches them by falling back
	// to date parsing once datetime parsing has failed, which is why a bad
	// *time* ("…T25:00:00Z") is reported as extra characters after a date
	// rather than as a bad hour: the date in front of it parsed, and the rest
	// is what the date parser calls trailing junk.
	momentPrefix       = "Input should be a valid datetime or date, "
	momentTooShort     = momentPrefix + "input is too short"
	momentBadYear      = momentPrefix + "invalid character in year"
	momentBadSeparator = momentPrefix + "invalid date separator, expected `-`"
	momentBadMonthChar = momentPrefix + "invalid character in month"
	momentBadMonth     = momentPrefix + "month value is outside expected range of 1-12"
	momentBadDayChar   = momentPrefix + "invalid character in day"
	momentBadDay       = momentPrefix + "day value is outside expected range"
	momentTrailing     = momentPrefix + "unexpected extra characters at the end of the input"

	// Conversion, after a successful parse.
	momentYearZero = "Input should be a valid datetime, year 0 is out of range"
	momentTooLate  = "Input should be a valid datetime, dates after 9999 are not supported as unix timestamps"
	momentTooEarly = "Input should be a valid datetime, dates before 0000 are not supported as unix timestamps"
)

// momentMillisecondThreshold is where a timestamp stops being seconds and
// starts being milliseconds. 20,000,000,000 itself is seconds (the year 2603);
// one more than it is milliseconds (August 1970). There is no third unit --
// 20,000,000,000,000 is milliseconds too, and so answers 2603 again -- and the
// boundary is on the magnitude, so it holds for negative timestamps.
const momentMillisecondThreshold = 20_000_000_000

// momentIntegerLimit is where a whole JSON number stops being readable as a
// timestamp at all: at 1e18 and beyond pydantic answers "Input should be a
// valid datetime" rather than a range refusal, because the number is no longer
// one it will take as an integer. Below it, a number out of range is refused
// as a date too late or too early.
const momentIntegerLimit = 1_000_000_000_000_000_000

// momentDigitLimit is how many significant digits of a numeric *string*
// pydantic will read as a timestamp. Nineteen it reads and refuses for its
// range; twenty it no longer treats as a timestamp at all, and falls back to
// parsing the text as a date -- which for a run of digits complains about the
// separator it wanted at the fifth character.
const momentDigitLimit = 19

// momentOpaqueDigits is where this reader stops agreeing with pydantic, and
// the only place it does.
//
// A numeric *string* carrying a dot whose whole part has this many significant
// digits or more is read by pydantic as something no divisor explains. Two
// measured examples, both from testdata/moment_fuzz.json:
//
//	"+2874861264401073.121" -> 2061-02-05T20:34:24.474242Z
//	"374341560324439.27896" -> 1981-11-11T15:46:00.763270Z
//
// Divide either by a thousand or a million and the seconds land where pydantic
// put them while the fraction does not, so it is not a unit this reader could
// pick differently; it looks like a fixed-width accumulator overflowing inside
// speedate. This reader refuses such a value for its range instead, which is
// what pydantic answers for the same digits without a dot.
//
// The exposure is a body field spelled as a thirteen-digit timestamp with a
// fraction -- a date in the year 3000 or beyond, written as a number of
// milliseconds, as a JSON string. No client of this API sends one. It is
// recorded here, in custom/docs/go-api-tier.md and in moment_test.go rather
// than left for the parity run to find, because a divergence nobody wrote down
// is the kind this port exists to avoid.
const momentOpaqueDigits = 13

// MaybeMoment is a `datetime | None` field of a partial-update model.
type MaybeMoment struct {
	Present bool
	// Value is nil for an explicit null.
	Value *time.Time
	// Naive is true when the text carried no offset and no Z, which pydantic
	// reads as a naive datetime. The moment in Value is then that wall-clock
	// time in UTC, because that is what the database is handed: asyncpg sends
	// a naive datetime for a timestamptz column as UTC. Routes that need to
	// tell the two apart have this; routes that only store it do not.
	Naive bool
}

// MaybeMoment reads an optional datetime as pydantic reads one.
func (m *Model) MaybeMoment(name string) MaybeMoment {
	value, present := m.body.Get(name)
	if !present {
		return MaybeMoment{}
	}
	if value == nil {
		return MaybeMoment{Present: true}
	}
	moment, naive, failure := ReadMoment(value)
	if failure != "" {
		m.reject(failure, name)
		return MaybeMoment{Present: true}
	}
	return MaybeMoment{Present: true, Value: &moment, Naive: naive}
}

// ReadMoment is pydantic's datetime coercion for one value: the moment it
// reads, whether that moment was written without an offset, and the refusal
// otherwise.
//
// The three paths, in the order pydantic takes them:
//
//	a number            -> a unix timestamp, seconds or milliseconds
//	a numeric string    -> the same, so "1700000000" and "20260304" are
//	                       timestamps and not dates
//	any other string    -> ISO 8601, and on failure the date parser's error
//
// Anything else -- true, false, a list, an object -- is NotADateTime.
func ReadMoment(value any) (time.Time, bool, string) {
	switch typed := value.(type) {
	case bool:
		// Before the number cases on purpose: pydantic reads true as 1 for an
		// int field but refuses it for a datetime.
		return time.Time{}, false, NotADateTime
	case pythonjson.RawInt:
		return fromNumber(string(typed))
	case float64:
		return fromFloat(typed)
	case string:
		if digits, fraction, negative, ok := numericLiteral(typed); ok {
			if len(strings.TrimLeft(digits, "0")) > momentDigitLimit {
				// Not a timestamp to pydantic any more: it goes back to
				// reading the text as a date.
				return fromISO(typed)
			}
			return fromDecimal(digits, fraction, negative, true)
		}
		return fromISO(typed)
	default:
		_ = typed
		return time.Time{}, false, NotADateTime
	}
}

// numericLiteral is the shape pydantic will read as a timestamp: an optional
// sign, digits, and at most one dot. No exponent ("1e12" is not a timestamp as
// a *string*, though it is as a JSON number), no underscores, no whitespace,
// no hex, no "inf" or "nan" -- each of those falls through to the ISO path and
// is refused there, usually as "input is too short".
func numericLiteral(text string) (digits, fraction string, negative, ok bool) {
	rest := text
	if rest != "" && (rest[0] == '+' || rest[0] == '-') {
		negative = rest[0] == '-'
		rest = rest[1:]
	}
	whole, fractionPart, hasDot := strings.Cut(rest, ".")
	if !allDigits(whole) || !allDigits(fractionPart) {
		return "", "", false, false
	}
	if len(whole)+len(fractionPart) == 0 {
		return "", "", false, false
	}
	if hasDot && strings.Contains(fractionPart, ".") {
		return "", "", false, false
	}
	return whole, fractionPart, negative, true
}

func allDigits(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// fromNumber reads a whole JSON number as a timestamp. The text is parsed
// rather than a float, because a JSON integer reaches this tier as text
// (internal/pythonjson.RawInt) precisely so that no precision is lost.
func fromNumber(text string) (time.Time, bool, string) {
	digits, fraction, negative, ok := numericLiteral(text)
	if !ok {
		return time.Time{}, false, NotADateTime
	}
	// A whole number of 1e18 or more is not an integer pydantic will read as a
	// timestamp, and it says so in different words from a range refusal:
	// 200000000000000000 is a date too late, 1000000000000000000 is not a
	// datetime at all. Nineteen significant digits is exactly that boundary.
	if len(strings.TrimLeft(digits, "0")) >= 19 {
		return time.Time{}, false, NotADateTime
	}
	return fromDecimal(digits, fraction, negative, false)
}

// fromFloat reads a fractional JSON number.
//
// It goes through a float because that is what pydantic has by then, and the
// precision already lost is part of the answer: 2874861264401073.121 arrives
// as a whole number of milliseconds and is refused, while the same digits as a
// *string* keep their fraction.
//
// A negative float is floored rather than negated, which is the one rule here
// that no reading of the code would have produced: -7534.289 as a number is
// -7535 + 0.289, so 1969-12-31T21:54:25.289 -- while the same text in quotes
// is -7534.289 exactly, so …:25.711. Two paths, two answers, both measured.
func fromFloat(value float64) (time.Time, bool, string) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return time.Time{}, false, NotADateTime
	}
	whole, fraction := math.Modf(value)
	// Wider than an int64 holds: converting it would wrap and answer a date
	// before year 1 for a value far in the future, so the sign decides.
	if math.Abs(whole) >= 9.2e18 {
		return time.Time{}, false, rangeRefusal(value < 0, false)
	}
	return assemble(int64(math.Abs(whole)), math.Abs(fraction), value < 0, true, false)
}

// fromDecimal turns a digit string and its fraction into the moment without
// going through a float, so a long fraction is read exactly.
func fromDecimal(digits, fraction string, negative, fromText bool) (time.Time, bool, string) {
	whole := int64(0)
	for i := 0; i < len(digits); i++ {
		whole = whole*10 + int64(digits[i]-'0')
		if whole < 0 {
			// Wider than an int64: past every range pydantic allows.
			return time.Time{}, false, rangeRefusal(negative, fromText)
		}
	}
	// Nine digits is past anything a microsecond can hold at any unit, and
	// keeps the float below exact.
	if len(fraction) > 9 {
		fraction = fraction[:9]
	}
	part := 0.0
	scale := 1.0
	for i := 0; i < len(fraction); i++ {
		scale /= 10
		part += float64(fraction[i]-'0') * scale
	}
	return assemble(whole, part, negative, false, fromText)
}

// assemble puts a magnitude, a fraction of its unit and a sign together.
//
// The unit is decided by the magnitude -- seconds up to the threshold,
// milliseconds past it -- and the fraction sits at that unit's place value, so
// the .5 of 20000000000000.5 is half a millisecond and not half a second. The
// rounding happens once, at the end, in microseconds: that is why
// 368350580460.6179 answers .460618 and rounding the fraction first would
// answer .460617.
func assemble(whole int64, fraction float64, negative, floorNegative, fromText bool) (time.Time, bool, string) {
	seconds, nanos := whole, int64(0)
	step := int64(1_000_000_000)
	if whole > momentMillisecondThreshold {
		seconds = whole / 1000
		nanos = (whole % 1000) * 1_000_000
		step = 1_000_000
	}
	// The fraction in nanoseconds, rounded at microseconds as a Python
	// datetime is.
	fractionNanos := int64(math.Round(fraction*float64(step)/1000)) * 1000

	if negative {
		seconds, nanos = -seconds, -nanos
		if floorNegative && fractionNanos != 0 {
			// pydantic floors the whole part and keeps the fraction positive.
			nanos += fractionNanos - step
		} else {
			nanos -= fractionNanos
		}
	} else {
		nanos += fractionNanos
	}

	moment := time.Unix(seconds, nanos).UTC()
	if moment.Year() > 9999 {
		return time.Time{}, false, rangeRefusal(false, fromText)
	}
	if moment.Year() < 1 {
		return time.Time{}, false, rangeRefusal(true, fromText)
	}
	// A timestamp is always an aware datetime in UTC.
	return moment, false, ""
}

// rangeRefusal is the range message, which carries "or date" when the value
// came from a string: pydantic reaches it there through its fall-back to date
// parsing, and words it accordingly.
func rangeRefusal(negative, fromText bool) string {
	switch {
	case negative && fromText:
		return momentPrefix + "dates before 0000 are not supported as unix timestamps"
	case negative:
		return momentTooEarly
	case fromText:
		return momentPrefix + "dates after 9999 are not supported as unix timestamps"
	default:
		return momentTooLate
	}
}
