package livechanges

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// The hub's own choices: how it is meant to run, not a deployment's settings.
const (
	// retained is how many recent changes each listening account keeps for
	// a reconnect to replay. A notebook whose folder of fifty files is
	// indexing writes a few hundred (each file moves through its stages);
	// a client that drops for a few seconds in the middle of that resumes
	// rather than reloading every list.
	retained = 512
	// linger is how long an account's recent changes are kept after its
	// last stream closes: a phone switched away and back, or a laptop
	// whose Wi-Fi blinked, resumes instead of reloading. Longer than that
	// and the client reloads, which is what it does on any fresh start.
	linger = 2 * time.Minute
	// subscriberBuffer is how many frames one stream may fall behind
	// before it is dropped. A stream that far behind is a client that has
	// stopped reading; dropping it frees the hub, and the client's
	// reconnect replays from the account's recent changes.
	subscriberBuffer = 256
)

// Frame is one change as a stream sends it: the change and the id a client
// sends back as Last-Event-ID.
type Frame struct {
	ID     string
	Change livechange.Change
}

// Hub fans one API process's share of the bus out to the streams open on it,
// per account. It is in memory and per process on purpose: a stream is a
// connection to this process, and nothing about it needs to outlive it.
//
// Every change gets a sequence number from one counter, and an id that is
// this process's epoch plus that number. A reconnect naming an id from the
// same epoch can be replayed from the account's recent changes; any other id
// (another replica, a restarted process) cannot, and the client is told to
// reload instead (Subscribe's resumed). That is exact rather than hopeful:
// the account records the highest number it can no longer vouch for.
type Hub struct {
	epoch string
	now   func() time.Time

	mu       sync.Mutex
	seq      uint64
	accounts map[string]*account
}

type account struct {
	streams map[*Subscription]struct{}
	// recent holds this account's changes after `complete`, oldest first.
	recent []entry
	// complete is the highest sequence number for which the account's
	// changes are NOT all in recent: those trimmed off, and everything
	// before the account was first listened to (or before the bus last
	// broke). A client that saw a change after it can be replayed.
	complete uint64
	// idleSince is when the last stream closed; zero while one is open.
	idleSince time.Time
}

type entry struct {
	seq    uint64
	change livechange.Change
}

// Subscription is one open stream's place in the hub.
type Subscription struct {
	hub    *Hub
	userID string
	frames chan Frame
	// dropped is closed when the hub lets go of the stream (it fell too far
	// behind, or the bus broke): the route ends the response and the
	// client reconnects.
	dropped chan struct{}
	once    sync.Once
}

// Frames is where the stream's changes arrive.
func (s *Subscription) Frames() <-chan Frame { return s.frames }

// Dropped is closed when the stream must end.
func (s *Subscription) Dropped() <-chan struct{} { return s.dropped }

func (s *Subscription) drop() { s.once.Do(func() { close(s.dropped) }) }

// NewHub is a hub with a fresh epoch. now is nil for the system clock.
func NewHub(now func() time.Time) *Hub {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand does not fail on a supported platform; were it to,
		// a constant epoch would let a reconnect to a restarted process
		// claim a replay it cannot have, so this stops the process instead.
		panic("livechanges: no randomness for the hub's epoch: " + err.Error())
	}
	if now == nil {
		now = time.Now
	}
	return &Hub{epoch: hex.EncodeToString(raw), now: now, accounts: map[string]*account{}}
}

// Subscribe opens a stream for the account. lastEventID is what the client
// sent as Last-Event-ID, or "". resumed reports whether replay is every
// change the client missed since then; when it is false the client must
// reload everything it shows live.
func (h *Hub) Subscribe(userID, lastEventID string) (sub *Subscription, replay []Frame, resumed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweep()
	acc := h.accounts[userID]
	if acc == nil {
		acc = &account{streams: map[*Subscription]struct{}{}, complete: h.seq}
		h.accounts[userID] = acc
	}
	if last, ok := h.sequenceOf(lastEventID); ok && last >= acc.complete {
		resumed = true
		for _, e := range acc.recent {
			if e.seq > last {
				replay = append(replay, h.frame(e))
			}
		}
	}
	sub = &Subscription{hub: h, userID: userID, frames: make(chan Frame, subscriberBuffer), dropped: make(chan struct{})}
	acc.streams[sub] = struct{}{}
	acc.idleSince = time.Time{}
	return sub, replay, resumed
}

// Close removes the stream. The account's recent changes stay for linger.
func (s *Subscription) Close() {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	acc := h.accounts[s.userID]
	if acc == nil {
		return
	}
	delete(acc.streams, s)
	if len(acc.streams) == 0 {
		acc.idleSince = h.now()
	}
}

// Deliver hands one message from the bus to the account's streams. An
// account nobody is listening to (and nobody was, lately) keeps nothing: the
// hub costs memory only for the people who have Soloclef open.
func (h *Hub) Deliver(message livechange.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	acc := h.accounts[message.UserID]
	if acc != nil && len(acc.streams) == 0 && acc.idleSince.Before(h.now().Add(-linger)) {
		// Gone quiet for good: forgotten here as sweep would, so an idle
		// account receiving changes does not keep collecting them until
		// the next subscribe somewhere sweeps it.
		delete(h.accounts, message.UserID)
		acc = nil
	}
	if acc == nil {
		// Still numbered: the count is what proves, at a later subscribe,
		// that a client's last id is older than changes it never heard.
		h.seq += uint64(len(message.Changes))
		return
	}
	for _, change := range message.Changes {
		h.seq++
		e := entry{seq: h.seq, change: change}
		acc.recent = append(acc.recent, e)
		if over := len(acc.recent) - retained; over > 0 {
			acc.complete = acc.recent[over-1].seq
			acc.recent = append(acc.recent[:0], acc.recent[over:]...)
		}
		frame := h.frame(e)
		for sub := range acc.streams {
			select {
			case sub.frames <- frame:
			default:
				// Never block the bus for one slow reader: drop it.
				sub.drop()
			}
		}
	}
}

// Gap is called when the bus may have lost messages (its subscription broke
// and was made again). Nothing the hub holds can be vouched for any more, so
// every account forgets its recent changes and every stream is ended: each
// client reconnects, is told it did not resume, and reloads.
func (h *Hub) Gap() {
	h.mu.Lock()
	defer h.mu.Unlock()
	// One number for the changes nobody heard: an id a client holds from
	// before the gap is then older than it, and never resumes.
	h.seq++
	for _, acc := range h.accounts {
		acc.recent = nil
		acc.complete = h.seq
		for sub := range acc.streams {
			sub.drop()
		}
	}
}

// Listening is how many streams are open, for the process's log and tests.
func (h *Hub) Listening() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, acc := range h.accounts {
		n += len(acc.streams)
	}
	return n
}

// sweep forgets accounts idle past linger. Run under the lock, on each
// Subscribe: the map only grows when someone subscribes, so that is when it
// needs pruning, and no timer goroutine is needed.
func (h *Hub) sweep() {
	cutoff := h.now().Add(-linger)
	for userID, acc := range h.accounts {
		if len(acc.streams) == 0 && acc.idleSince.Before(cutoff) {
			delete(h.accounts, userID)
		}
	}
}

func (h *Hub) frame(e entry) Frame {
	return Frame{ID: h.epoch + "-" + strconv.FormatUint(e.seq, 10), Change: e.change}
}

// sequenceOf reads an id this hub issued; any other id is not ours.
func (h *Hub) sequenceOf(id string) (uint64, bool) {
	epoch, number, found := strings.Cut(id, "-")
	if !found || epoch != h.epoch {
		return 0, false
	}
	seq, err := strconv.ParseUint(number, 10, 64)
	if err != nil || seq > h.seq {
		return 0, false
	}
	return seq, true
}
