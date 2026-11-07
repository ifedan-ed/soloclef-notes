//go:build !linux

package broker

import (
	"errors"
	"time"
)

// ReadMonotonic has no implementation off Linux. The worker runs on Linux; the
// honest answer elsewhere is that queue wait cannot be measured, so the metric
// is absent rather than invented.
func ReadMonotonic() (time.Duration, error) {
	return 0, errors.New("broker: CLOCK_MONOTONIC is only read on linux")
}
