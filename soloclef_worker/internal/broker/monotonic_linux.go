//go:build linux

package broker

import (
	"syscall"
	"time"
	"unsafe"
)

// ReadMonotonic reads CLOCK_MONOTONIC directly, because that is the clock
// Python's time.monotonic_ns reads and the producer's enqueue stamp is in its
// terms. Go's own time package keeps a monotonic reading but does not expose
// its absolute value, which is the one thing needed to compare with a stamp
// taken in another process.
func ReadMonotonic() (time.Duration, error) {
	var ts syscall.Timespec
	if _, _, errno := syscall.Syscall(
		syscall.SYS_CLOCK_GETTIME, clockMonotonic, uintptr(unsafe.Pointer(&ts)), 0,
	); errno != 0 {
		return 0, errno
	}
	return time.Duration(ts.Nano()), nil
}

const clockMonotonic = 1
