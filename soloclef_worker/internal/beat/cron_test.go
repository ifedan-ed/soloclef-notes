package beat

import (
	"testing"
	"time"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("bad test time %q: %v", value, err)
	}
	return parsed.UTC()
}

func compile(t *testing.T, spec Crontab) Cron {
	t.Helper()
	cron, err := spec.Compile()
	if err != nil {
		t.Fatalf("Compile(%+v): %v", spec, err)
	}
	return cron
}

// Every form app/celery_app.py uses, read as Celery's crontab reads it.
func TestCronMatchesTheFormsTheBeatScheduleUses(t *testing.T) {
	cases := []struct {
		name  string
		spec  Crontab
		when  string
		match bool
	}{
		{"every minute", Crontab{Minute: "*"}, "2026-11-08T13:47:00Z", true},
		{"every five, on", Crontab{Minute: "*/5"}, "2026-11-08T13:45:00Z", true},
		{"every five, off", Crontab{Minute: "*/5"}, "2026-11-08T13:47:00Z", false},
		{"minute seven", Crontab{Minute: "7"}, "2026-11-08T13:07:00Z", true},
		{"minute seven, wrong minute", Crontab{Minute: "7"}, "2026-11-08T13:08:00Z", false},
		{"hour and minute", Crontab{Minute: "50", Hour: "5"}, "2026-11-08T05:50:00Z", true},
		{"hour and minute, wrong hour", Crontab{Minute: "50", Hour: "5"}, "2026-11-08T06:50:00Z", false},
		// 2026-11-08 is a Sunday, which Celery numbers 0.
		{"sunday sweep", Crontab{Minute: "0", Hour: "6", DayOfWeek: "0"}, "2026-11-08T06:00:00Z", true},
		{"sunday sweep, monday", Crontab{Minute: "0", Hour: "6", DayOfWeek: "0"}, "2026-11-09T06:00:00Z", false},
		{"every other hour", Crontab{Minute: "0", Hour: "*/2"}, "2026-11-08T04:00:00Z", true},
		{"every other hour, odd", Crontab{Minute: "0", Hour: "*/2"}, "2026-11-08T05:00:00Z", false},
		{"a list", Crontab{Minute: "0,30"}, "2026-11-08T05:30:00Z", true},
		{"a range", Crontab{Minute: "10-12"}, "2026-11-08T05:11:00Z", true},
		{"a range, outside", Crontab{Minute: "10-12"}, "2026-11-08T05:13:00Z", false},
		{"a stepped range", Crontab{Minute: "10-20/5"}, "2026-11-08T05:15:00Z", true},
		{"a day of the month", Crontab{Minute: "0", Hour: "0", DayOfMonth: "8"}, "2026-11-08T00:00:00Z", true},
		{"a month", Crontab{Minute: "0", Hour: "0", MonthOfYear: "11"}, "2026-11-08T00:00:00Z", true},
		{"the wrong month", Crontab{Minute: "0", Hour: "0", MonthOfYear: "10"}, "2026-11-08T00:00:00Z", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compile(t, tc.spec).Matches(at(t, tc.when)); got != tc.match {
				t.Errorf("Matches(%s) = %v, want %v", tc.when, got, tc.match)
			}
		})
	}
}

// Celery ANDs day_of_month and day_of_week, where Vixie cron ORs them. The
// difference is invisible until an entry sets both, so it is pinned here.
func TestCronAndsTheDayFieldsAsCeleryDoes(t *testing.T) {
	// 2026-11-08 is a Sunday and the 8th; 2026-11-15 is a Sunday.
	cron := compile(t, Crontab{Minute: "0", Hour: "0", DayOfMonth: "8", DayOfWeek: "0"})
	if !cron.Matches(at(t, "2026-11-08T00:00:00Z")) {
		t.Error("a Sunday the 8th satisfies both fields and must match")
	}
	if cron.Matches(at(t, "2026-11-15T00:00:00Z")) {
		t.Error("a Sunday that is not the 8th must not match: Celery ands the day fields")
	}
}

func TestCronNextIsTheNextMatchingMinute(t *testing.T) {
	cases := []struct {
		name string
		spec Crontab
		from string
		want string
	}{
		{"every minute", Crontab{Minute: "*"}, "2026-11-08T13:47:00Z", "2026-11-08T13:48:00Z"},
		{"within the second", Crontab{Minute: "*"}, "2026-11-08T13:47:30Z", "2026-11-08T13:48:00Z"},
		{"every five", Crontab{Minute: "*/5"}, "2026-11-08T13:47:00Z", "2026-11-08T13:50:00Z"},
		{"next hour", Crontab{Minute: "7"}, "2026-11-08T13:07:00Z", "2026-11-08T14:07:00Z"},
		{"next day", Crontab{Minute: "41", Hour: "3"}, "2026-11-08T03:41:00Z", "2026-11-09T03:41:00Z"},
		{"next sunday", Crontab{Minute: "0", Hour: "6", DayOfWeek: "0"}, "2026-11-08T06:00:00Z", "2026-11-15T06:00:00Z"},
		{"next year", Crontab{Minute: "0", Hour: "0", DayOfMonth: "1", MonthOfYear: "1"}, "2026-11-08T00:00:00Z", "2027-01-01T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compile(t, tc.spec).Next(at(t, tc.from))
			if !got.Equal(at(t, tc.want)) {
				t.Errorf("Next(%s) = %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

// A schedule that can never fire is a schedule that silently stops working, so
// it is refused at startup rather than waited on.
func TestCronRefusesASpecItCannotRun(t *testing.T) {
	cases := map[string]Crontab{
		"minute out of range": {Minute: "60"},
		"negative":            {Minute: "-1"},
		"not a number":        {Minute: "monday"},
		"zero step":           {Minute: "*/0"},
		"backwards range":     {Minute: "20-10"},
		"empty":               {Minute: ""},
		"hour out of range":   {Minute: "0", Hour: "24"},
		"day zero":            {Minute: "0", DayOfMonth: "0"},
		"month thirteen":      {Minute: "0", MonthOfYear: "13"},
		// Celery numbers the week 0-6 with Sunday 0, and rejects 7 for
		// Sunday even though cron accepts it.
		"sunday as seven": {Minute: "0", DayOfWeek: "7"},
		"weekday eight":   {Minute: "0", DayOfWeek: "8"},
		"unreachable day": {Minute: "0", Hour: "0", DayOfMonth: "31", MonthOfYear: "2"},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := spec.Compile(); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}

// A field left empty is Celery's own default for that field: crontab(minute=7)
// runs every hour of every day.
func TestCronDefaultsUnsetFieldsToEveryValue(t *testing.T) {
	cron := compile(t, Crontab{Minute: "7"})
	for _, when := range []string{"2026-01-01T00:07:00Z", "2026-11-08T23:07:00Z"} {
		if !cron.Matches(at(t, when)) {
			t.Errorf("Matches(%s) = false", when)
		}
	}
}
