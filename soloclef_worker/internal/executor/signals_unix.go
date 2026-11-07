//go:build unix

package executor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ownProcessGroup makes the child the leader of a process group of its own, so
// everything it starts can be signalled with it. A task body here spawns
// ffmpeg and other tools; signalling the Python process alone would leave an
// encoder running with nothing watching it.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// askGroupToStop sends SIGTERM to the child's whole group. The group id is the
// child's own pid, because ownProcessGroup made it the leader, and the group
// outlives the leader for as long as any member is still running — which is
// what makes a grandchild reachable after the body itself has exited.
func askGroupToStop(pid int) error { return signalGroup(pid, syscall.SIGTERM) }

// killGroup ends the group with no say in the matter.
func killGroup(pid int) error { return signalGroup(pid, syscall.SIGKILL) }

// raiseSoftLimit is the soft time limit as Celery's own prefork pool delivers
// it: SIGUSR1, which the task process turns into SoftTimeLimitExceeded inside
// the body so its except and finally blocks still run. It goes to the process
// alone, not the group: nothing else in the group has a handler for it, and
// SIGUSR1 unhandled is fatal.
func raiseSoftLimit(pid int) error {
	if pid <= 0 {
		return errors.New("executor: no process to signal")
	}
	return syscall.Kill(pid, syscall.SIGUSR1)
}

func signalGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return errors.New("executor: no process group to signal")
	}
	return syscall.Kill(-pid, sig)
}

// terminationSignal reports the signal that ended a process. An OOM kill
// arrives this way, as signal 9 with nothing of the body's own having run.
func terminationSignal(state *os.ProcessState) (number int, name string, killed bool) {
	if state == nil {
		return 0, "", false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0, "", false
	}
	sig := status.Signal()
	return int(sig), sig.String(), true
}
