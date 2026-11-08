package markdownmemory

import (
	"strconv"
	"time"
)

// isoDate is date.fromisoformat() for the ten-character text a memory bullet
// carries, which is where the parser reads a date from.
//
// Ten characters is two spellings, not one. Python 3.11's date.fromisoformat
// also reads an ISO *week* date, and "2026-W01-1" is exactly ten characters —
// so a bullet written `- 2026-W01-1: text` parses and is re-rendered as
// `- 2025-12-29: text`. That is not a theory: it is what the running Python
// tier answered, and "2026-W53-7" came back as "2027-01-03".
//
// Only the ten-character spellings matter here, because every caller slices ten
// characters out before asking. The compact forms ("20260101", "2026W011") are
// eight and are therefore never offered — checked, and such a bullet is kept as
// a raw line on both tiers.
func isoDate(text string) (time.Time, bool) {
	if len(text) != 10 {
		return time.Time{}, false
	}
	year, ok := asciiNumber(text[0:4])
	if !ok || text[4] != '-' {
		return time.Time{}, false
	}
	if text[5] == 'W' {
		return isoWeekDate(year, text)
	}
	return isoCalendarDate(year, text)
}

// isoCalendarDate reads "YYYY-MM-DD".
func isoCalendarDate(year int, text string) (time.Time, bool) {
	month, monthOK := asciiNumber(text[5:7])
	day, dayOK := asciiNumber(text[8:10])
	if !monthOK || !dayOK || text[7] != '-' {
		return time.Time{}, false
	}
	if month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
}

// isoWeekDate reads "YYYY-Www-D": the week-numbering year, the ISO week and the
// weekday, where 1 is Monday.
func isoWeekDate(year int, text string) (time.Time, bool) {
	week, weekOK := asciiNumber(text[6:8])
	weekday, dayOK := asciiNumber(text[9:10])
	if !weekOK || !dayOK || text[8] != '-' {
		return time.Time{}, false
	}
	if week < 1 || week > isoWeeksInYear(year) || weekday < 1 || weekday > 7 {
		return time.Time{}, false
	}
	// The Monday of week 1 is the Monday of the week holding 4 January.
	fourth := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	offset := int(fourth.Weekday())
	if offset == 0 {
		offset = 7 // Sunday is 7 in ISO, 0 in Go.
	}
	firstMonday := fourth.AddDate(0, 0, 1-offset)
	return firstMonday.AddDate(0, 0, (week-1)*7+weekday-1), true
}

// isoWeeksInYear is 53 for a long ISO year and 52 otherwise, which is what
// rejects "2026-W53-7" for a year with 52 weeks.
func isoWeeksInYear(year int) int {
	if isoLongYear(year) {
		return 53
	}
	return 52
}

// isoLongYear: a year has 53 ISO weeks when it starts on a Thursday, or is a
// leap year starting on a Wednesday.
func isoLongYear(year int) bool {
	first := int(time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC).Weekday())
	if first == 0 {
		first = 7
	}
	return first == 4 || (first == 3 && isLeapYear(year))
}

func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeapYear(year) {
			return 29
		}
		return 28
	}
	return 0
}

// asciiNumber accepts ASCII digits only, as date.fromisoformat does: a bullet
// dated with Arabic-Indic digits is a raw line on both tiers.
func asciiNumber(text string) (int, bool) {
	for index := 0; index < len(text); index++ {
		if text[index] < '0' || text[index] > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	return value, true
}

// renderDate is date.isoformat(), which is always the calendar spelling — so a
// week date read above comes back out as a plain one.
func renderDate(day time.Time) string {
	return day.Format("2006-01-02")
}
