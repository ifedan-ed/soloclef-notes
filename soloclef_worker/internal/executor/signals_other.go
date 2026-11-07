//go:build !unix

package executor

import (
	"errors"
	"os"
	"os/exec"
)

// This worker runs on Linux. The stubs keep the package building elsewhere and
// refuse rather than pretend: a platform where a task's children cannot be
// signalled as a group cannot run tasks safely.
var errNoProcessGroups = errors.New("executor: process groups are only supported on unix")

func ownProcessGroup(*exec.Cmd) {}

func askGroupToStop(int) error { return errNoProcessGroups }

func killGroup(int) error { return errNoProcessGroups }

func raiseSoftLimit(int) error { return errNoProcessGroups }

func terminationSignal(*os.ProcessState) (int, string, bool) { return 0, "", false }
