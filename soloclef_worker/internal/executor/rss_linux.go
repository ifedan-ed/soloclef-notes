//go:build linux

package executor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// residentBytes is how much memory a process is really holding.
// /proc/<pid>/statm rather than ps: a read, not a process, so it costs nothing
// to ask after every job.
func residentBytes(pid int) (uint64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, fmt.Errorf("executor: /proc/%d/statm reads %q", pid, raw)
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("executor: resident pages of %d: %w", pid, err)
	}
	return pages * uint64(os.Getpagesize()), nil
}
