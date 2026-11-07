package pythonjson

import (
	"testing"
	"time"
)

func TestPydanticDateTimeSpellsAZeroOffsetZ(t *testing.T) {
	// Captured from the Python tier's member list, which renders through a
	// response_model: a zero offset is "Z", and a fraction is always six
	// digits. ISOFormat's "+00:00" would be the wrong bytes on that route.
	for _, example := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 1, 5, 0, 0, 3, 0, time.UTC), "2026-01-05T00:00:03Z"},
		{time.Date(2026, 2, 10, 11, 22, 33, 500_000_000, time.UTC), "2026-02-10T11:22:33.500000Z"},
		{time.Date(2026, 11, 1, 8, 30, 15, 123_456_000, time.UTC), "2026-11-01T08:30:15.123456Z"},
		{time.Date(2026, 3, 1, 0, 0, 0, 1000, time.UTC), "2026-03-01T00:00:00.000001Z"},
	} {
		if got := PydanticDateTime(example.at); got != example.want {
			t.Errorf("got %q, want %q", got, example.want)
		}
	}
}

func TestPydanticDateTimeMovesAnOffsetMomentToUTC(t *testing.T) {
	// pgx hands back a timestamptz in the connection's timezone; Python always
	// sees UTC, so the offset has to be resolved before it is spelled.
	at := time.Date(2026, 5, 4, 15, 0, 0, 0, time.FixedZone("plus-two", 2*60*60))
	if got := PydanticDateTime(at); got != "2026-05-04T13:00:00Z" {
		t.Errorf("got %q", got)
	}
}
