package pythonjson

import (
	"testing"
	"time"
)

func TestISOFormatMatchesPythonsIsoformat(t *testing.T) {
	// The want values are datetime.isoformat() for the same moments, as the
	// Python tier's own responses print them.
	for want, moment := range map[string]time.Time{
		"2026-10-21T14:47:07.278766+00:00": time.Date(2026, 10, 21, 14, 47, 7, 278766000, time.UTC),
		"2026-09-20T23:07:44.864526+00:00": time.Date(2026, 9, 20, 23, 7, 44, 864526000, time.UTC),
		"2026-10-11T04:04:18+00:00":        time.Date(2026, 10, 11, 4, 4, 18, 0, time.UTC),
		"2026-10-11T04:04:18.000001+00:00": time.Date(2026, 10, 11, 4, 4, 18, 1000, time.UTC),
	} {
		if got := ISOFormat(moment); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestISOFormatNormalisesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*60*60)
	moment := time.Date(2026, 10, 21, 16, 47, 7, 278766000, zone)
	const want = "2026-10-21T14:47:07.278766+00:00"
	if got := ISOFormat(moment); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
