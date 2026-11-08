package beat

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Crontab is one of app/celery_app.py's crontab(...) specs, written the same
// way: a field left empty is "*", which is what Celery's own crontab defaults
// every field but the minute to.
type Crontab struct {
	Minute      string
	Hour        string
	DayOfWeek   string
	DayOfMonth  string
	MonthOfYear string
}

// Cron is a compiled Crontab. Everything is evaluated in UTC, because
// app/celery_app.py sets enable_utc=True and timezone="UTC": the schedule has
// no local time and so nothing to be moved by a daylight-saving change.
type Cron struct {
	minute, hour, dayOfMonth, month, dayOfWeek uint64
	spec                                       Crontab
}

// searchHorizon bounds the scan for the next firing. A spec that matches
// nothing inside it — the 31st of February — is a schedule that would wait
// forever, so Compile refuses it instead of leaving an entry that never runs.
const searchHorizon = 5 * 366 * 24 * time.Hour

// Compile reads the spec, or says why it could never fire.
func (c Crontab) Compile() (Cron, error) {
	cron := Cron{spec: c}
	fields := []struct {
		raw      string
		min, max int
		name     string
		into     *uint64
	}{
		{c.Minute, 0, 59, "minute", &cron.minute},
		{c.Hour, 0, 23, "hour", &cron.hour},
		{c.DayOfMonth, 1, 31, "day_of_month", &cron.dayOfMonth},
		{c.MonthOfYear, 1, 12, "month_of_year", &cron.month},
		{c.DayOfWeek, 0, 6, "day_of_week", &cron.dayOfWeek},
	}
	for _, field := range fields {
		raw := strings.TrimSpace(field.raw)
		if raw == "" {
			if field.name == "minute" {
				// Celery's crontab takes minute="*" only when it is given;
				// an entry with no minute at all is a schedule nobody wrote.
				return Cron{}, fmt.Errorf("beat: a crontab needs a minute")
			}
			raw = "*"
		}
		bits, err := parseCronField(raw, field.min, field.max)
		if err != nil {
			return Cron{}, fmt.Errorf("beat: %s %q: %w", field.name, raw, err)
		}
		*field.into = bits
	}
	if cron.Next(time.Unix(0, 0).UTC()).IsZero() {
		return Cron{}, fmt.Errorf("beat: %+v matches no time in the next five years", c)
	}
	return cron, nil
}

// Matches reports whether a minute is one this schedule fires in. Seconds are
// ignored: a crontab's resolution is the minute.
func (c Cron) Matches(when time.Time) bool {
	at := when.UTC()
	return c.minute&(1<<at.Minute()) != 0 &&
		c.hour&(1<<at.Hour()) != 0 &&
		c.month&(1<<int(at.Month())) != 0 &&
		// Celery ands the two day fields where Vixie cron ors them.
		c.dayOfMonth&(1<<at.Day()) != 0 &&
		c.dayOfWeek&(1<<int(at.Weekday())) != 0
}

// Next is the first minute strictly after the one given that this schedule
// fires in, or the zero time if there is none inside searchHorizon.
func (c Cron) Next(after time.Time) time.Time {
	at := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := after.UTC().Add(searchHorizon)
	for at.Before(limit) {
		switch {
		case c.month&(1<<int(at.Month())) == 0:
			at = startOfNextMonth(at)
		case c.dayOfMonth&(1<<at.Day()) == 0 || c.dayOfWeek&(1<<int(at.Weekday())) == 0:
			at = startOfNextDay(at)
		case c.hour&(1<<at.Hour()) == 0:
			at = startOfNextHour(at)
		case c.minute&(1<<at.Minute()) == 0:
			at = at.Add(time.Minute)
		default:
			return at
		}
	}
	return time.Time{}
}

// String is the spec as app/celery_app.py wrote it, which is what a log line
// about an entry should say.
func (c Cron) String() string {
	return fmt.Sprintf("minute=%s hour=%s day_of_week=%s day_of_month=%s month_of_year=%s",
		orStar(c.spec.Minute), orStar(c.spec.Hour), orStar(c.spec.DayOfWeek),
		orStar(c.spec.DayOfMonth), orStar(c.spec.MonthOfYear))
}

func orStar(field string) string {
	if strings.TrimSpace(field) == "" {
		return "*"
	}
	return field
}

func startOfNextMonth(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
}

func startOfNextDay(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

func startOfNextHour(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), 0, 0, 0, time.UTC).Add(time.Hour)
}

// parseCronField reads one comma-separated field in the forms Celery's
// crontab accepts as numbers: "*", "*/n", "a", "a-b" and "a-b/n". Names
// ("mon", "january") are not accepted, because nothing in this app's schedule
// uses one and a silently misread name would fire on the wrong day.
func parseCronField(raw string, min, max int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("empty element")
		}

		step := 1
		if base, stepRaw, stepped := strings.Cut(part, "/"); stepped {
			parsed, err := strconv.Atoi(strings.TrimSpace(stepRaw))
			if err != nil || parsed < 1 {
				return 0, fmt.Errorf("step %q is not a positive number", stepRaw)
			}
			part, step = strings.TrimSpace(base), parsed
		}

		low, high := min, max
		if part != "*" {
			var err error
			if lowRaw, highRaw, ranged := strings.Cut(part, "-"); ranged {
				if low, err = cronNumber(lowRaw, min, max); err != nil {
					return 0, err
				}
				if high, err = cronNumber(highRaw, min, max); err != nil {
					return 0, err
				}
				if low > high {
					return 0, fmt.Errorf("range %q runs backwards", part)
				}
			} else {
				if low, err = cronNumber(part, min, max); err != nil {
					return 0, err
				}
				high = low
			}
		}
		for value := low; value <= high; value += step {
			bits |= 1 << value
		}
	}
	return bits, nil
}

func cronNumber(raw string, min, max int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", strings.TrimSpace(raw))
	}
	if value < min || value > max {
		return 0, fmt.Errorf("%d is outside %d-%d", value, min, max)
	}
	return value, nil
}
