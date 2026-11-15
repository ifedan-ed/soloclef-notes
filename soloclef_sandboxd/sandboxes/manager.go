// Package sandboxes is the life of a sandbox: one container per chat thread
// or per render attempt, found by its key, counted against its account,
// kept alive while used and removed once idle for its time to live.
//
// # One service per stack
//
// The service is the stack's only maker of sandboxes and there is one of it,
// so the registry here is the truth and needs no lock across processes: the
// per-account count and "find or create" are decided under one mutex, not
// asked of the daemon and raced between replicas as they were with
// OpenSandbox. Every container also carries the stack's label and the
// service looks at no other container: two stacks on one Docker never see
// each other's sandboxes. (OpenSandbox restored timers for every sandbox on
// the host at start, any server's, and would expire another stack's renewed
// sandbox early.)
//
// An account at its limit can be waited on: a request that says how long it
// will wait is parked until one of the account's sandboxes is removed, in
// the order the requests came (waiting.go).
//
// # Expiry without renewals
//
// A sandbox is in use while any call on it is in flight, and is removed
// when it has been idle longer than its time to live. A command that runs
// for twenty minutes keeps its sandbox by running; the clients send no
// renewals and keep no timers.
package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/docker"
)

// Engine is what the manager needs of Docker (*docker.Client).
type Engine interface {
	Create(ctx context.Context, name string, spec docker.ContainerSpec) (string, error)
	Start(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	List(ctx context.Context, labels ...string) ([]docker.Container, error)
	Exec(ctx context.Context, container string, options docker.ExecOptions) (int, error)
}

// Kind is what a sandbox belongs to.
type Kind string

const (
	// Thread is a chat thread's sandbox: it keeps its files between turns
	// and counts against its account's limit.
	//
	// It is the one kind. A queued video once had a "job" kind of its own,
	// a sandbox per render attempt outside the account's limit; videos are
	// drawn by the worker now, with no sandbox, and the kind went with them.
	// The kind stays in the protocol and the container's name so that a
	// sandbox is still found by what it belongs to, not by a bare number.
	Thread Kind = "thread"
)

// ErrGone is a sandbox that no longer exists: removed, expired, or its
// container died.
var ErrGone = errors.New("the sandbox no longer exists")

// ErrBadRequest is a request the manager refuses as malformed: an unknown
// kind, a key or account that is not label-safe, a missing limit.
var ErrBadRequest = errors.New("bad request")

// ErrAccountFull is an account that already has as many thread sandboxes as
// its request allows, and that freed none within the request's wait (or
// already has MaximumWaiting requests waiting: ErrTooManyWaiting).
var ErrAccountFull = errors.New("the account has reached its limit of sandboxes")

// keyPattern is what a thread's key may be: label- and
// name-safe, so it can neither break the container's name nor be mistaken
// for another key.
var keyPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// accountPattern is an account's id as the clients send it (a lower-cased
// uuid, or a test's short name).
var accountPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Request asks for the sandbox of one thread.
type Request struct {
	Kind    Kind
	Key     string
	Account string
	// NanoCPUs and MemoryBytes are the sandbox's hard limits (ParseCPU,
	// ParseMemory).
	NanoCPUs    int64
	MemoryBytes int64
	// IdleTTL is how long the sandbox outlives its last use.
	IdleTTL time.Duration
	// MaxPerAccount is the account's limit of thread sandboxes.
	MaxPerAccount int
	// MaxWait is how long the request waits for a place when its account
	// is at its limit (waiting.go); zero is ErrAccountFull at once.
	MaxWait time.Duration
}

// MinimumIdleTTL is the shortest time to live a request may ask for. Below
// it a sandbox could expire between two tool calls of one turn.
const MinimumIdleTTL = time.Minute

func (r Request) check() error {
	switch r.Kind {
	case Thread:
		if r.MaxPerAccount < 1 {
			return errors.New("a thread's sandbox needs the account's limit (at least 1)")
		}
	default:
		return fmt.Errorf("kind %q is not thread", r.Kind)
	}
	if !keyPattern.MatchString(r.Key) {
		return fmt.Errorf("key %q is not lower-case letters, digits and inner hyphens", r.Key)
	}
	if !accountPattern.MatchString(r.Account) {
		return fmt.Errorf("account %q is not an account's id", r.Account)
	}
	if r.NanoCPUs <= 0 || r.MemoryBytes <= 0 {
		return errors.New("a sandbox needs its CPU and memory limits")
	}
	if r.IdleTTL < MinimumIdleTTL {
		return fmt.Errorf("the idle time to live must be at least %s", MinimumIdleTTL)
	}
	if r.MaxWait < 0 || r.MaxWait > MaximumWait {
		return fmt.Errorf("the wait for a place must be between none and %s", MaximumWait)
	}
	return nil
}

// Image is the sandbox image and the facts about it a container needs.
type Image struct {
	// Reference is the image's name on the daemon (the stack's own build).
	Reference string
	// Path is PATH for every command: the image's, with the interpreter
	// that carries the document libraries first.
	Path string
	// User is the uid:gid commands run as: not root, and not the keeper's.
	User string
	uid  int
	gid  int
}

// userPattern is a numeric uid:gid. Names would be looked up in the image's
// /etc/passwd at every exec; numbers are what the tmpfs owners need anyway.
var userPattern = regexp.MustCompile(`^([1-9][0-9]{0,8}):([1-9][0-9]{0,8})$`)

// Manager makes, finds and removes one stack's sandboxes.
type Manager struct {
	engine Engine
	stack  string
	image  Image
	log    *slog.Logger
	now    func() time.Time

	mu    sync.Mutex
	byKey map[string]*sandbox // kind + "/" + key
	byID  map[string]*sandbox
	// places is the accounts with requests waiting for a place in their
	// limit (waiting.go).
	places map[string]*places
}

// sandbox is one sandbox in the registry.
type sandbox struct {
	id      string
	kind    Kind
	key     string
	account string
	idleTTL time.Duration
	// lastUsed is when the last call on it started or ended; busy is how
	// many are in flight. Idle means busy == 0, for longer than idleTTL.
	lastUsed time.Time
	busy     int
	// ready is closed once the container is started (or failed to be);
	// until then the entry holds the account's slot and the key, so a
	// second request for the same thread waits for this one instead of
	// creating another.
	ready  chan struct{}
	failed error
}

func registryKey(kind Kind, key string) string { return string(kind) + "/" + key }

// stackPattern is a stack's name: a Compose project's.
var stackPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// New returns the manager of one stack's sandboxes. It knows of none until
// Adopt is called.
func New(engine Engine, stack string, image Image, log *slog.Logger) (*Manager, error) {
	if !stackPattern.MatchString(stack) {
		return nil, fmt.Errorf("stack %q is not a Compose project's name", stack)
	}
	if image.Reference == "" || image.Path == "" {
		return nil, errors.New("the sandbox image and its PATH are required")
	}
	ids := userPattern.FindStringSubmatch(image.User)
	if ids == nil {
		return nil, fmt.Errorf("the sandbox user %q is not a numeric uid:gid other than root", image.User)
	}
	image.uid, _ = strconv.Atoi(ids[1])
	image.gid, _ = strconv.Atoi(ids[2])
	return &Manager{
		engine: engine, stack: stack, image: image, log: log, now: time.Now,
		byKey: map[string]*sandbox{}, byID: map[string]*sandbox{}, places: map[string]*places{},
	}, nil
}

// Acquire returns the id of the sandbox for the request's thread,
// creating it if there is none, and marks it used. A thread's account at
// its limit waits for a place for as long as the request allows
// (waiting.go), and is ErrAccountFull when that is no time or runs out.
func (m *Manager) Acquire(ctx context.Context, request Request) (id string, created bool, err error) {
	if err := request.check(); err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	key := registryKey(request.Kind, request.Key)
	for {
		m.mu.Lock()
		if existing := m.byKey[key]; existing != nil {
			m.mu.Unlock()
			select {
			case <-existing.ready:
			case <-ctx.Done():
				return "", false, ctx.Err()
			}
			m.mu.Lock()
			if m.byKey[key] == existing && existing.failed == nil {
				existing.lastUsed = m.now()
				m.mu.Unlock()
				return existing.id, false, nil
			}
			// It failed to start or was removed meanwhile: look again.
			m.mu.Unlock()
			continue
		}
		if !m.mustWait(request) {
			break // m.mu stays held for the reservation
		}
		if request.MaxWait == 0 {
			m.mu.Unlock()
			return "", false, ErrAccountFull
		}
		w, err := m.enqueue(request)
		m.mu.Unlock()
		if err != nil {
			return "", false, err
		}
		if err := m.await(ctx, request, w); err != nil {
			return "", false, err
		}
		// m.mu is held again, with a place promised to this request.
		m.takeUp(request.Account)
		if m.byKey[key] == nil {
			break // the place becomes this thread's sandbox, under the same lock
		}
		// Another request for the same thread made its sandbox while this
		// one waited: that sandbox is the answer, and the place goes to
		// whoever waits next.
		m.handOut(request.Account)
		m.mu.Unlock()
	}

	entry := &sandbox{
		kind: request.Kind, key: request.Key, account: request.Account, idleTTL: request.IdleTTL,
		lastUsed: m.now(), ready: make(chan struct{}),
	}
	m.byKey[key] = entry
	m.mu.Unlock()

	id, err = m.start(ctx, request)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		entry.failed = err
		delete(m.byKey, key)
		// Its place is free again, for whoever waits for one.
		m.handOut(entry.account)
	} else {
		entry.id = id
		entry.lastUsed = m.now()
		m.byID[id] = entry
	}
	close(entry.ready)
	return id, err == nil, err
}

// threadsOf counts an account's thread sandboxes, those still starting
// included. m.mu is held.
func (m *Manager) threadsOf(account string) int {
	count := 0
	for _, entry := range m.byKey {
		if entry.kind == Thread && entry.account == account {
			count++
		}
	}
	return count
}

// start creates and starts the container. A container that was created but
// would not start is removed again, so it cannot hold the name.
func (m *Manager) start(ctx context.Context, request Request) (string, error) {
	id, err := m.engine.Create(ctx, m.containerName(request.Kind, request.Key), m.spec(request))
	if err != nil {
		return "", fmt.Errorf("creating the sandbox: %w", err)
	}
	if err := m.engine.Start(ctx, id); err != nil {
		m.removeQuietly(id)
		return "", fmt.Errorf("starting the sandbox: %w", err)
	}
	return id, nil
}

// Release removes the sandbox of a thread, if it has one. Calls in
// flight on it fail with ErrGone.
func (m *Manager) Release(ctx context.Context, kind Kind, key string) error {
	m.mu.Lock()
	entry := m.byKey[registryKey(kind, key)]
	m.mu.Unlock()
	if entry == nil {
		return nil
	}
	select {
	case <-entry.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if entry.failed != nil {
		return nil
	}
	return m.remove(ctx, entry)
}

// remove removes a sandbox's container, then forgets it. Forgotten only once
// it is gone, so a failed removal leaves it in the registry to be tried
// again by the next sweep instead of running on unseen.
func (m *Manager) remove(ctx context.Context, entry *sandbox) error {
	if err := m.engine.Remove(ctx, entry.id); err != nil {
		return fmt.Errorf("removing sandbox %s: %w", entry.id, err)
	}
	m.forget(entry)
	return nil
}

// forget drops a sandbox from the registry, if it is still there.
func (m *Manager) forget(entry *sandbox) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byID[entry.id] == entry {
		delete(m.byID, entry.id)
	}
	if key := registryKey(entry.kind, entry.key); m.byKey[key] == entry {
		delete(m.byKey, key)
		// The account has a place free: the first of its waiters takes it.
		m.handOut(entry.account)
	}
}

// removeQuietly removes a container whose failure is already being
// reported, with a context of its own so a cancelled request still cleans
// up.
func (m *Manager) removeQuietly(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.engine.Remove(ctx, id); err != nil {
		m.log.Warn("sandbox: removing a container failed", "container", id, "error", err)
	}
}

// use marks a sandbox busy for one call and returns the function that ends
// the call. An id the registry does not have is ErrGone.
func (m *Manager) use(id string) (*sandbox, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.byID[id]
	if entry == nil {
		return nil, nil, ErrGone
	}
	entry.busy++
	entry.lastUsed = m.now()
	return entry, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		entry.busy--
		entry.lastUsed = m.now()
	}, nil
}

// lost handles an engine error from a call on a sandbox: a container the
// daemon no longer has, or that is no longer running, is removed and
// forgotten, and the call answers ErrGone.
func (m *Manager) lost(entry *sandbox, err error) error {
	if docker.IsNotFound(err) || docker.IsConflict(err) {
		m.removeQuietly(entry.id)
		m.forget(entry)
		return fmt.Errorf("%w (%v)", ErrGone, err)
	}
	return err
}

// Sweep removes every sandbox idle for longer than its time to live. It is
// called on a timer (SweepUntil).
func (m *Manager) Sweep(ctx context.Context) {
	now := m.now()
	var expired []*sandbox
	m.mu.Lock()
	for _, entry := range m.byID {
		if entry.busy == 0 && now.Sub(entry.lastUsed) > entry.idleTTL {
			expired = append(expired, entry)
		}
	}
	m.mu.Unlock()
	for _, entry := range expired {
		if err := m.remove(ctx, entry); err != nil {
			m.log.Warn("sandbox: removing an idle sandbox failed; it is tried again next sweep", "container", entry.id, "error", err)
		}
	}
}

// sweepEvery is how often idle sandboxes are looked for. A sandbox may
// outlive its time to live by this much, which against a time to live of
// minutes is nothing.
const sweepEvery = 10 * time.Second

// SweepUntil sweeps every sweepEvery until ctx ends.
func (m *Manager) SweepUntil(ctx context.Context) {
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Sweep(ctx)
		}
	}
}

// Adopt rebuilds the registry from the daemon: every running container of
// this stack is taken back with a full time to live from now (its last use
// before this service restarted is not known, and ending a sandbox early
// would lose a chat's files), and every one that is not running, or whose
// labels are not this service's, is removed.
func (m *Manager) Adopt(ctx context.Context) error {
	found, err := m.engine.List(ctx, labelStack+"="+m.stack)
	if err != nil {
		return fmt.Errorf("listing this stack's sandboxes: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, container := range found {
		entry, ok := m.fromLabels(container)
		if !ok || container.State != "running" || m.byKey[registryKey(entry.kind, entry.key)] != nil {
			if err := m.engine.Remove(ctx, container.ID); err != nil {
				return fmt.Errorf("removing a stopped sandbox: %w", err)
			}
			continue
		}
		m.byKey[registryKey(entry.kind, entry.key)] = entry
		m.byID[entry.id] = entry
	}
	return nil
}

// fromLabels reads a registry entry back from a container's labels.
func (m *Manager) fromLabels(container docker.Container) (*sandbox, bool) {
	labels := container.Labels
	seconds, err := strconv.ParseInt(labels[labelIdleTTL], 10, 64)
	entry := &sandbox{
		id: container.ID, kind: Kind(labels[labelKind]), key: labels[labelKey], account: labels[labelAccount],
		idleTTL: time.Duration(seconds) * time.Second, lastUsed: m.now(), ready: make(chan struct{}),
	}
	close(entry.ready)
	valid := err == nil && entry.idleTTL >= MinimumIdleTTL && entry.kind == Thread &&
		keyPattern.MatchString(entry.key) && accountPattern.MatchString(entry.account)
	return entry, valid
}

// Count is how many sandboxes the registry holds, for the health answer and
// the tests.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}
