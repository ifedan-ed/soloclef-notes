package publicchat

import (
	"testing"
	"time"
)

// TestALinkIsExpiredAtTheInstantItExpires pins the one comparison no recorded
// case can: app/public_sharing/expiry.py:is_expired is `expires_at <= now`,
// and the recorded expiries are a month either side of the recording, so a
// `<` here would pass every one of them and serve a link for the microsecond
// the Python tier has already stopped.
func TestALinkIsExpiredAtTheInstantItExpires(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, c := range []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		{"no expiry", nil, false},
		{"a microsecond ago", pointer(now.Add(-time.Microsecond)), true},
		{"this instant", pointer(now), true},
		{"a microsecond from now", pointer(now.Add(time.Microsecond)), false},
	} {
		if got := expired(c.expiresAt, now); got != c.want {
			t.Errorf("%s: expired is %v, want %v", c.name, got, c.want)
		}
	}
}

func pointer[T any](value T) *T { return &value }
