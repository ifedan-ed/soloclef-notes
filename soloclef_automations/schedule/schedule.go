// Package schedule reads a five-field cron expression in a named time zone
// and answers when it next fires.
//
// The fields are minute, hour, day of month, month and day of week, each a
// list of values, ranges and steps ("*/15", "1-5", "0,30", "mon-fri" is not
// taken: numbers only, 0 or 7 for Sunday). When both day fields are
// restricted a moment matches if either does, which is what cron has always
// done and what the schedule builder's "weekdays" and "day of month" rely
// on.
//
// The search walks the calendar in the schedule's own zone, so "09:00 every
// weekday" stays 09:00 across a daylight-saving change. A wall-clock time
// the change skips (02:30 on the night clocks go forward) does not fire that
// day; one the change repeats fires once, at its first occurrence.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a compiled expression in its zone.
type Schedule struct {
	minute, hour, dayOfMonth, month, dayOfWeek uint64
	// dayOfMonthAny and dayOfWeekAny say the field was "*": cron's rule for
	// the two day fields depends on which were restricted.
	dayOfMonthAny, dayOfWeekAny bool
	zone                        *time.Location
}

// horizon bounds the search. An expression that matches nothing within it
// (the 30th of February) would wait for ever, so Parse refuses it rather
// than save a trigger that never fires.
const horizon = 5 * 366 * 24 * time.Hour

// Parse compiles cron in the IANA zone timezone, or says what is wrong in
// words for the person who wrote it.
func Parse(cron, timezone string) (Schedule, error) {
	if strings.TrimSpace(timezone) == "" {
		return Schedule{}, fmt.Errorf("a schedule needs a time zone")
	}
	zone, err := time.LoadLocation(timezone)
	if err != nil {
		return Schedule{}, fmt.Errorf("unknown time zone %q", timezone)
	}
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("a schedule has five fields (minute hour day-of-month month day-of-week), got %d in %q", len(fields), cron)
	}
	s := Schedule{zone: zone, dayOfMonthAny: fields[2] == "*", dayOfWeekAny: fields[4] == "*"}
	for i, f := range []struct {
		name     string
		min, max int
		into     *uint64
	}{
		{"minute", 0, 59, &s.minute}, {"hour", 0, 23, &s.hour}, {"day of month", 1, 31, &s.dayOfMonth},
		{"month", 1, 12, &s.month}, {"day of week", 0, 7, &s.dayOfWeek},
	} {
		bits, err := parseField(fields[i], f.min, f.max)
		if err != nil {
			return Schedule{}, fmt.Errorf("the %s field %q: %w", f.name, fields[i], err)
		}
		*f.into = bits
	}
	// 7 is Sunday too.
	if s.dayOfWeek&(1<<7) != 0 {
		s.dayOfWeek |= 1
	}
	if _, found := s.search(time.Now()); !found {
		return Schedule{}, fmt.Errorf("the schedule %q never fires", cron)
	}
	return s, nil
}

func parseField(field string, min, max int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		spec, step := part, 1
		if before, after, stepped := strings.Cut(part, "/"); stepped {
			n, err := strconv.Atoi(after)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("the step %q is not a positive number", after)
			}
			spec, step = before, n
		}
		low, high := min, max
		switch {
		case spec == "*":
		case strings.Contains(spec, "-"):
			a, b, _ := strings.Cut(spec, "-")
			var errA, errB error
			low, errA = strconv.Atoi(a)
			high, errB = strconv.Atoi(b)
			if errA != nil || errB != nil {
				return 0, fmt.Errorf("%q is not a range of numbers", spec)
			}
		default:
			n, err := strconv.Atoi(spec)
			if err != nil {
				return 0, fmt.Errorf("%q is not a number", spec)
			}
			low, high = n, n
			if step > 1 {
				// "5/15" is cron for "from 5, every 15".
				high = max
			}
		}
		if low < min || high > max || low > high {
			return 0, fmt.Errorf("%d-%d is outside %d-%d", low, high, min, max)
		}
		for v := low; v <= high; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

// Next is the first moment strictly after `after` that the schedule fires,
// in UTC.
func (s Schedule) Next(after time.Time) time.Time {
	next, _ := s.search(after)
	return next
}

func (s Schedule) search(after time.Time) (time.Time, bool) {
	// The next whole minute, on the schedule's own wall clock.
	t := after.In(s.zone).Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(horizon)
	for t.Before(limit) {
		switch {
		case s.month&(1<<uint(t.Month())) == 0:
			// To the first minute of the next month.
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, s.zone)
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, s.zone)
		case s.hour&(1<<uint(t.Hour())) == 0:
			// By absolute time, so a day with a repeated or missing hour is
			// walked hour by hour as it happens.
			t = t.Truncate(time.Hour).Add(time.Hour)
		case s.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// dayMatches is cron's rule for the two day fields: both must hold when
// either is "*", and either may hold when both are restricted.
func (s Schedule) dayMatches(t time.Time) bool {
	dom := s.dayOfMonth&(1<<uint(t.Day())) != 0
	dow := s.dayOfWeek&(1<<uint(t.Weekday())) != 0
	if s.dayOfMonthAny || s.dayOfWeekAny {
		return dom && dow
	}
	return dom || dow
}
