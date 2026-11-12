package requestbody

import "time"

// The ISO 8601 half of pydantic's datetime reader.
//
// It is written as speedate parses, not as time.Parse does, because the
// difference is observable in the refusals: every field is a fixed width, the
// date is required to be at least ten bytes before anything else is looked at,
// and a failure anywhere after the date is reported as trailing characters
// after a date rather than as the thing that was actually wrong. Matching
// time.RFC3339 instead would accept "2026-3-4T5:06:07Z" and reject
// "2026-03-04 05:06:07", and both would be the wrong answer.

// fromISO reads an ISO 8601 date or datetime. The returned bool is whether the
// text carried no offset, which pydantic reads as a naive datetime.
func fromISO(text string) (time.Time, bool, string) {
	// Ten bytes is the shortest thing that can be a date, and the length is
	// checked before any character is, so "abc" and "2026-3-4" are both "too
	// short" rather than a complaint about their contents.
	if len(text) < 10 {
		return time.Time{}, false, momentTooShort
	}
	year, ok := twoPairsOfDigits(text[0:4])
	if !ok {
		return time.Time{}, false, momentBadYear
	}
	if text[4] != '-' {
		return time.Time{}, false, momentBadSeparator
	}
	month, ok := pairOfDigits(text[5:7])
	if !ok {
		// A character that is not a digit is a different complaint from a
		// number out of range, and pydantic words both.
		return time.Time{}, false, momentBadMonthChar
	}
	if month < 1 || month > 12 {
		return time.Time{}, false, momentBadMonth
	}
	if text[7] != '-' {
		return time.Time{}, false, momentBadSeparator
	}
	day, ok := pairOfDigits(text[8:10])
	if !ok {
		return time.Time{}, false, momentBadDayChar
	}
	if day < 1 || day > daysIn(year, month) {
		return time.Time{}, false, momentBadDay
	}
	if year == 0 {
		// The date parsed; Python's datetime cannot hold it. pydantic words
		// this one without the "or date" prefix.
		return time.Time{}, false, momentYearZero
	}

	// A date on its own is midnight, naive.
	if len(text) == 10 {
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true, ""
	}

	// Anything after the date has to start with a separator pydantic accepts.
	// Four do: the ISO "T", a lowercase "t", a space and an underscore.
	switch text[10] {
	case 'T', 't', ' ', '_':
	default:
		return time.Time{}, false, momentTrailing
	}
	hour, minute, second, nanos, offset, naive, ok := isoTime(text[11:])
	if !ok {
		// The date in front of it parsed, so pydantic's fall-back to date
		// parsing succeeds and then complains about everything after it.
		return time.Time{}, false, momentTrailing
	}

	zone := time.UTC
	if !naive && offset != 0 {
		zone = time.FixedZone("", offset)
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, nanos, zone), naive, ""
}

// isoTime reads "HH:MM[:SS[.ffffff]][Z|±HH:MM|±HHMM]" and nothing else.
//
// Every field is exactly two digits: "T5:06:07Z" is not a time here, which is
// what makes "2026-03-04T5:06:07Z" a refusal on both tiers.
func isoTime(text string) (hour, minute, second, nanos, offset int, naive, ok bool) {
	if len(text) < 5 {
		return 0, 0, 0, 0, 0, false, false
	}
	hour, ok = pairOfDigits(text[0:2])
	if !ok || hour > 23 {
		return 0, 0, 0, 0, 0, false, false
	}
	if text[2] != ':' {
		return 0, 0, 0, 0, 0, false, false
	}
	minute, ok = pairOfDigits(text[3:5])
	if !ok || minute > 59 {
		return 0, 0, 0, 0, 0, false, false
	}
	rest := text[5:]

	// Seconds are optional: "2026-03-04T05:06" is a datetime.
	if len(rest) >= 3 && rest[0] == ':' {
		second, ok = pairOfDigits(rest[1:3])
		if !ok || second > 59 {
			return 0, 0, 0, 0, 0, false, false
		}
		rest = rest[3:]

		// A fraction follows a dot or a comma, and at least one digit has to
		// follow it: "05:06:07." is a refusal.
		if len(rest) > 0 && (rest[0] == '.' || rest[0] == ',') {
			digits := 0
			scale := 1_000_000_000
			for digits+1 <= len(rest)-1 {
				character := rest[digits+1]
				if character < '0' || character > '9' {
					break
				}
				// Truncated at microseconds, as pydantic truncates: thirteen
				// digits of fraction answer six.
				if scale > 1000 {
					scale /= 10
					nanos += int(character-'0') * scale
				}
				digits++
			}
			if digits == 0 {
				return 0, 0, 0, 0, 0, false, false
			}
			rest = rest[digits+1:]
		}
	}

	if rest == "" {
		// No offset at all: a naive datetime.
		return hour, minute, second, nanos, 0, true, true
	}
	offset, ok = isoOffset(rest)
	if !ok {
		return 0, 0, 0, 0, 0, false, false
	}
	return hour, minute, second, nanos, offset, false, true
}

// isoOffset reads "Z", "z", "±HH:MM" or "±HHMM", and nothing else. An
// hour-only "+02" is not an offset here, which is pydantic's answer too.
func isoOffset(text string) (int, bool) {
	if text == "Z" || text == "z" {
		return 0, true
	}
	if len(text) != 5 && len(text) != 6 {
		return 0, false
	}
	sign := 1
	switch text[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	hours, ok := pairOfDigits(text[1:3])
	if !ok || hours > 23 {
		return 0, false
	}
	rest := text[3:]
	if len(rest) == 3 {
		if rest[0] != ':' {
			return 0, false
		}
		rest = rest[1:]
	}
	minutes, ok := pairOfDigits(rest)
	if !ok || minutes > 59 {
		return 0, false
	}
	return sign * (hours*3600 + minutes*60), true
}

func pairOfDigits(text string) (int, bool) {
	if len(text) != 2 {
		return 0, false
	}
	if text[0] < '0' || text[0] > '9' || text[1] < '0' || text[1] > '9' {
		return 0, false
	}
	return int(text[0]-'0')*10 + int(text[1]-'0'), true
}

func twoPairsOfDigits(text string) (int, bool) {
	if len(text) != 4 {
		return 0, false
	}
	value := 0
	for i := 0; i < 4; i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
		value = value*10 + int(text[i]-'0')
	}
	return value, true
}

// daysIn is the length of one month, with the Gregorian leap rule.
func daysIn(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	}
	return 0
}
