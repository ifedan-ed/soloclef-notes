package api

import (
	"fmt"
	"math"
	"time"
)

// A body's durations are whole numbers of a unit (waitSeconds,
// idleTtlSeconds, timeoutMs), and time.Duration counts nanoseconds in an
// int64. Multiplied without a check, a large number wraps: 10^10 seconds
// is a negative wait, and some larger ones wrap back to a small positive
// duration the manager would accept as if it had been asked for. So each
// number is checked as an integer, against the most its field may say,
// before it becomes a duration, and a number out of range is refused
// rather than cut down to the bound: the caller has a bug, and a silent
// clamp would hide it.

// maxWholeUnits is the most whole units of unit a time.Duration can hold.
func maxWholeUnits(unit time.Duration) int64 {
	return int64(math.MaxInt64 / unit)
}

// durationOf turns n units into a duration, refusing a negative n and one
// above most, which must itself fit in a duration. name is the body's field,
// for the message.
func durationOf(name string, n int64, unit time.Duration, most int64) (time.Duration, error) {
	if n < 0 || n > most {
		return 0, fmt.Errorf("%s must be a whole number from 0 to %d", name, most)
	}
	return time.Duration(n) * unit, nil
}
