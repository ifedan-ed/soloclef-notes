package sandboxes

import (
	"fmt"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/docker"
)

// The labels on every sandbox container. They are the service's whole record
// of a sandbox: on start it rebuilds its registry from them (adopt), so a
// restart of this service loses no sandbox and needs no database.
const (
	labelStack   = "soloclef.sandbox.stack"
	labelKind    = "soloclef.sandbox.kind"
	labelKey     = "soloclef.sandbox.key"
	labelAccount = "soloclef.sandbox.account"
	labelIdleTTL = "soloclef.sandbox.idle-ttl-seconds"
)

// The confinement every sandbox gets. None of these is a deployment's
// choice: they are what makes running a model's code safe, so they are
// fixed here, each with its reason, rather than offered as settings that
// could be loosened by accident.
const (
	// pidsLimit stops a fork bomb at the sandbox. LibreOffice and a Python
	// build use a few dozen processes at most; the headroom is for a
	// model's own subprocesses.
	pidsLimit = 4096
	// openFiles is the per-process descriptor limit. LibreOffice and the
	// document libraries hold a few hundred; the daemon's default (about a
	// million) only helps a runaway. It was 16384 for the Chromium of the
	// retired Remotion video render, which wanted a few thousand.
	openFiles = 4096
	// shmBytes is /dev/shm: Docker's default of 64 MiB, named here so it
	// is a decision. Python's multiprocessing uses it, and tmpfs pages
	// count against the sandbox's memory limit either way.
	shmBytes = 64 << 20
)

// writableDirs are the only places a sandbox can write: the root
// filesystem is read-only. Each is a tmpfs, so what is written lives in the
// sandbox's own memory and is bounded by its memory limit: a sandbox cannot
// fill the host's disk, which it could on a writable root (overlay2 on ext4
// has no per-container quota, and the host's disk is shared with
// everything).
//
// Each has a size of its own, as a share of the memory limit, adding up to
// three quarters of it plus the home's small fixed size. A full disk then
// answers "No space left on device", which a model can read and act on,
// while the last quarter stays for processes. Sized at the whole limit, a
// file that filled the memory would leave none for any process: every
// later command, `rm` included, would be killed as it started, and the
// sandbox would be lost with its files (measured: TestARealSandboxIsConfined).
//
//   - /workspace is where commands start, and where documents and their
//     output are written: half.
//   - /tmp is every library's scratch (LibreOffice, a deck's preview, the
//     tools' own scripts): a quarter.
//   - the home holds the caches LibreOffice, fontconfig and matplotlib
//     insist on, a few megabytes: homeBytes.
var writableDirs = []struct {
	path     string
	share    int64 // the mount's size is the memory limit divided by this; 0 for homeBytes
	everyone bool  // sticky and open to all, as /tmp is everywhere
}{
	{path: "/workspace", share: 2},
	{path: "/tmp", share: 4, everyone: true},
	{path: homeDir},
}

// homeDir is the sandbox user's home, on a tmpfs.
const homeDir = "/home/sandbox"

// homeBytes is the home's size. LibreOffice's profile, the largest thing
// put there, is about 10 MiB.
const homeBytes = 64 << 20

// keeperCommand is the container's main process: it only keeps the
// container alive, because every command arrives by exec. It runs as root
// (the image's user) under Docker's init, while commands run as Image.User,
// so a model's `pkill -u` or `kill -1` cannot end its own sandbox; root here
// has no capabilities (CapDrop ALL) and cannot gain any (no-new-privileges).
var keeperCommand = []string{"sleep", "infinity"}

// spec is the container a sandbox is: the image with nothing of its own
// entrypoint (the image's starts a Jupyter server the commands never use),
// confined as the constants above say.
func (m *Manager) spec(request Request) docker.ContainerSpec {
	uid, gid := m.image.uid, m.image.gid
	tmpfs := make(map[string]string, len(writableDirs))
	for _, dir := range writableDirs {
		size := int64(homeBytes)
		if dir.share > 0 {
			size = request.MemoryBytes / dir.share
		}
		options := fmt.Sprintf("size=%d,uid=%d,gid=%d,mode=0755", size, uid, gid)
		if dir.everyone {
			options = fmt.Sprintf("size=%d,mode=1777", size)
		}
		tmpfs[dir.path] = options
	}
	return docker.ContainerSpec{
		Image:      m.image.Reference,
		Entrypoint: keeperCommand,
		Cmd:        []string{},
		// The image's own environment stays (Docker merges this over it);
		// PATH is replaced with the one that puts the interpreter carrying
		// the document libraries first, which the image's entrypoint would
		// otherwise have chosen at start.
		Env: []string{
			"PATH=" + m.image.Path,
			"HOME=" + homeDir,
			"TMPDIR=/tmp",
		},
		WorkingDir: "/workspace",
		Labels: map[string]string{
			labelStack:   m.stack,
			labelKind:    string(request.Kind),
			labelKey:     request.Key,
			labelAccount: request.Account,
			labelIdleTTL: strconv.FormatInt(int64(request.IdleTTL.Seconds()), 10),
		},
		// No network at all, not even a loopback-only bridge: only the
		// container's own lo exists. So nothing in it can reach the stack's databases, the
		// cloud's metadata address, the private ranges or the internet,
		// by name or by address, and nothing has to be filtered: there
		// is no route to filter. This replaces OpenSandbox's egress
		// sidecar (a second container per sandbox doing DNS and nftables
		// filtering of a bridge it was still attached to).
		NetworkDisabled: true,
		HostConfig: docker.HostConfig{
			NetworkMode:    "none",
			ReadonlyRootfs: true,
			Tmpfs:          tmpfs,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			// Docker's init is PID 1: it reaps the orphans a killed
			// command leaves (LibreOffice forks), which
			// `sleep` as PID 1 would leave as zombies until the pids limit.
			Init:     true,
			NanoCPUs: request.NanoCPUs,
			Memory:   request.MemoryBytes,
			// Equal to Memory: no swap, so the limit is a hard bound on
			// processes and tmpfs files together.
			MemorySwap: request.MemoryBytes,
			PidsLimit:  pidsLimit,
			Ulimits:    []docker.Ulimit{{Name: "nofile", Soft: openFiles, Hard: openFiles}},
			ShmSize:    shmBytes,
			// The keeper prints nothing and commands' output comes back by
			// exec, so there is nothing to log.
			LogConfig:     docker.LogConfig{Type: "none"},
			RestartPolicy: docker.RestartPolicy{Name: "no"},
		},
	}
}

// containerName is a sandbox's name on the daemon: readable in `docker ps`,
// and unique per stack, kind and key, so the daemon itself refuses a second
// sandbox for one thread even if this service's registry
// were wrong.
func (m *Manager) containerName(kind Kind, key string) string {
	return m.stack + "-sandbox-" + string(kind) + "-" + key
}
