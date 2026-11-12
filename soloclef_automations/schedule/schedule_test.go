package schedule

import (
	"strings"
	"testing"
	"time"
)

func at(t *testing.T, text string) time.Time {
	t.Helper()
	moment, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return moment
}

func TestWhenAScheduleNextFires(t *testing.T) {
	for _, c := range []struct {
		name, cron, zone, after, want string
	}{
		{"every weekday at nine, from a Friday evening", "0 9 * * 1-5", "UTC", "2026-11-13T18:00:00Z", "2026-11-16T09:00:00Z"},
		{"strictly after: nine o'clock at nine is tomorrow", "0 9 * * *", "UTC", "2026-11-12T09:00:00Z", "2026-11-13T09:00:00Z"},
		{"every fifteen minutes", "*/15 * * * *", "UTC", "2026-11-12T09:07:30Z", "2026-11-12T09:15:00Z"},
		{"in the schedule's own zone", "30 8 * * *", "Africa/Lagos", "2026-11-12T08:00:00Z", "2026-11-13T07:30:00Z"},
		{"first of the month", "0 0 1 * *", "UTC", "2026-11-12T00:00:00Z", "2026-12-01T00:00:00Z"},
		{"Sunday as 7", "0 12 * * 7", "UTC", "2026-11-12T00:00:00Z", "2026-11-15T12:00:00Z"},
		{"either day field when both are restricted", "0 0 13 * 1", "UTC", "2026-11-12T00:00:00Z", "2026-11-13T00:00:00Z"},
		{"nine stays nine across clocks going back", "0 9 * * *", "Europe/London", "2026-10-24T12:00:00Z", "2026-10-25T09:00:00Z"},
		{"a skipped wall-clock time does not fire that night", "30 2 * * *", "America/New_York", "2026-03-07T12:00:00Z", "2026-03-09T06:30:00Z"},
		{"a leap day", "0 0 29 2 *", "UTC", "2026-11-12T00:00:00Z", "2028-02-29T00:00:00Z"},
	} {
		s, err := Parse(c.cron, c.zone)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := s.Next(at(t, c.after)); !got.Equal(at(t, c.want)) {
			t.Errorf("%s: next is %s, want %s", c.name, got.Format(time.RFC3339), c.want)
		}
	}
}

func TestAScheduleThatCannotRunIsRefusedInWords(t *testing.T) {
	for _, c := range []struct{ cron, zone, says string }{
		{"0 9 * *", "UTC", "five fields"},
		{"61 9 * * *", "UTC", "minute"},
		{"0 9 * * mon", "UTC", "day of week"},
		{"0 9 * * *", "Mars/Olympus", "unknown time zone"},
		{"0 9 * * *", "", "needs a time zone"},
		{"0 0 30 2 *", "UTC", "never fires"},
		{"*/0 * * * *", "UTC", "step"},
	} {
		_, err := Parse(c.cron, c.zone)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q in %q: %v, want an error saying %q", c.cron, c.zone, err, c.says)
		}
	}
}
