package pushpolicy

import (
	"errors"
	"testing"
	"time"
)

func at(t *testing.T, zone, clock string) time.Time {
	t.Helper()
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	instant, err := time.ParseInLocation("2006-01-02 15:04", clock, location)
	if err != nil {
		t.Fatal(err)
	}
	return instant
}

func quiet(t *testing.T, start, end, zone string) *QuietHours {
	t.Helper()
	s, err := ParseClock(start)
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseClock(end)
	if err != nil {
		t.Fatal(err)
	}
	q, err := NewQuietHours(s, e, zone)
	if err != nil {
		t.Fatal(err)
	}
	return &q
}

// The span includes its start, excludes its end, and crosses midnight when
// it starts later than it ends.
func TestQuietHoursCoverTheirSpanOnTheLocalClock(t *testing.T) {
	night := quiet(t, "22:00", "07:00", "Africa/Lagos")
	afternoon := quiet(t, "13:00", "14:30", "Africa/Lagos")
	cases := []struct {
		span  *QuietHours
		clock string
		want  bool
	}{
		{night, "2026-11-12 21:59", false},
		{night, "2026-11-12 22:00", true},
		{night, "2026-11-12 23:59", true},
		{night, "2026-11-13 00:00", true},
		{night, "2026-11-13 06:59", true},
		{night, "2026-11-13 07:00", false},
		{night, "2026-11-13 12:00", false},
		{afternoon, "2026-11-12 12:59", false},
		{afternoon, "2026-11-12 13:00", true},
		{afternoon, "2026-11-12 14:29", true},
		{afternoon, "2026-11-12 14:30", false},
	}
	for _, c := range cases {
		if got := c.span.Covers(at(t, "Africa/Lagos", c.clock)); got != c.want {
			t.Errorf("%s-%s at %s: %v, want %v", FormatClock(c.span.StartMinute), FormatClock(c.span.EndMinute), c.clock, got, c.want)
		}
	}
}

// The person's zone decides, not the server's: 23:00 in Lagos is 22:00 UTC,
// and the same instant is 17:00 in New York, outside a 22:00-07:00 span
// there.
func TestQuietHoursAreReadInThePersonsZone(t *testing.T) {
	instant := at(t, "UTC", "2026-11-12 22:00")
	if !quiet(t, "22:00", "07:00", "Africa/Lagos").Covers(instant) {
		t.Error("23:00 in Lagos should be quiet")
	}
	if quiet(t, "22:00", "07:00", "America/New_York").Covers(instant) {
		t.Error("17:00 in New York should not be quiet")
	}
}

// Across a summer-time change the span stays on the wall clock: 06:30 local
// is quiet the morning after the clocks go back in London.
func TestQuietHoursFollowTheWallClockAcrossSummerTime(t *testing.T) {
	span := quiet(t, "22:00", "07:00", "Europe/London")
	if !span.Covers(at(t, "Europe/London", "2026-10-25 06:30")) {
		t.Error("06:30 on the morning the clocks change should be quiet")
	}
	if span.Covers(at(t, "Europe/London", "2026-10-25 07:30")) {
		t.Error("07:30 should not be quiet")
	}
}

func TestOneSpellingOfATimeIsAccepted(t *testing.T) {
	good := map[string]int{"00:00": 0, "07:05": 425, "23:59": 1439, "12:00": 720}
	for text, want := range good {
		if got, err := ParseClock(text); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", text, got, err, want)
		}
		if FormatClock(want) != text {
			t.Errorf("FormatClock(%d) = %q", want, FormatClock(want))
		}
	}
	for _, bad := range []string{"", "7:00", "24:00", "23:60", "07:00:00", "0a:00", " 7:00", "-1:00", "07-00", "+7:00"} {
		if _, err := ParseClock(bad); !errors.Is(err, ErrClock) {
			t.Errorf("%q: %v, want ErrClock", bad, err)
		}
	}
}

// Nothing is guessed: an empty span, a zone the database does not know, and
// the server's own "Local" are refused rather than read as something.
func TestASpanThatCannotMeanOneThingIsRefused(t *testing.T) {
	if _, err := NewQuietHours(600, 600, "Africa/Lagos"); !errors.Is(err, ErrEmpty) {
		t.Errorf("an empty span: %v", err)
	}
	for _, zone := range []string{"", "Local", "Mars/Olympus", "Africa/ Lagos", "UTC+1"} {
		if _, err := NewQuietHours(0, 60, zone); !errors.Is(err, ErrZoneName) {
			t.Errorf("zone %q: %v", zone, err)
		}
	}
	if _, err := NewQuietHours(-1, 60, "UTC"); !errors.Is(err, ErrClock) {
		t.Errorf("a minute before midnight: %v", err)
	}
	if _, err := NewQuietHours(0, 1440, "UTC"); !errors.Is(err, ErrClock) {
		t.Errorf("a minute past the day: %v", err)
	}
}

func TestChoicesDefaultOnAndIgnoreWhatIsNotASwitch(t *testing.T) {
	choices := Choices([]byte(`{"output": false, "answer": false, "source": "no", "mail": true}`))
	if len(choices) != len(Kinds) {
		t.Fatalf("%d choices, want one per kind: %v", len(choices), choices)
	}
	want := map[string]bool{Output: false, Source: true, Automation: true, Mail: true}
	for kind, on := range want {
		if choices[kind] != on {
			t.Errorf("%s = %v, want %v", kind, choices[kind], on)
		}
	}
	if _, kept := choices["answer"]; kept {
		t.Error("a kind no longer offered must not come back")
	}
	if got := Choices([]byte(`not json`)); len(got) != len(Kinds) || !got[Output] {
		t.Errorf("unreadable choices should read as the defaults: %v", got)
	}
}

func TestDecideChecksTheSwitchThenQuietHours(t *testing.T) {
	night := quiet(t, "22:00", "07:00", "UTC")
	noon := at(t, "UTC", "2026-11-12 12:00")
	midnight := at(t, "UTC", "2026-11-12 23:30")
	on := Preferences{Choices: Choices(nil), Quiet: night}
	off := Preferences{Choices: Choices([]byte(`{"output": false}`)), Quiet: night}
	cases := []struct {
		name        string
		preferences Preferences
		kind        string
		now         time.Time
		want        Verdict
	}{
		{"on, daytime", on, Output, noon, Send},
		{"on, quiet", on, Output, midnight, QuietNow},
		{"off, daytime", off, Output, noon, SwitchedOff},
		{"off wins over quiet", off, Output, midnight, SwitchedOff},
		{"another kind still on", off, Source, noon, Send},
		{"no quiet hours", Preferences{Choices: Choices(nil)}, Automation, midnight, Send},
		{"an unknown kind", on, "answer", noon, UnknownKind},
	}
	for _, c := range cases {
		if got := Decide(c.preferences, c.kind, c.now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
