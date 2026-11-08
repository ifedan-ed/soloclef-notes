//go:build !linux

package monotonic

import (
	"errors"
	"time"
)

// Read has no implementation off Linux. Both tiers run on Linux; the honest
// answer elsewhere is that the stamp cannot be taken, so it is left off rather
// than invented.
func Read() (time.Duration, error) {
	return 0, errors.New("monotonic: CLOCK_MONOTONIC is only read on linux")
}
