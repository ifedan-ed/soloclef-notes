//go:build !linux

package executor

import "errors"

// residentBytes has no answer away from Linux, and a worker that cannot read a
// process's memory retires nothing for it rather than guessing.
func residentBytes(int) (uint64, error) {
	return 0, errors.New("executor: resident memory is only readable on Linux")
}
