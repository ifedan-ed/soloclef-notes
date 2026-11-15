package livechanges

import (
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func message(user string, kinds ...livechange.Kind) livechange.Message {
	m := livechange.Message{UserID: user}
	for _, k := range kinds {
		m.Changes = append(m.Changes, livechange.Change{Kind: k})
	}
	return m
}

func received(t *testing.T, sub *Subscription) []Frame {
	t.Helper()
	var frames []Frame
	for {
		select {
		case f := <-sub.Frames():
			frames = append(frames, f)
		default:
			return frames
		}
	}
}

func TestAnAccountHearsOnlyItsOwnChanges(t *testing.T) {
	hub := NewHub(nil)
	alice, _, _ := hub.Subscribe("alice", "")
	bob, _, _ := hub.Subscribe("bob", "")
	hub.Deliver(message("alice", livechange.Documents))
	hub.Deliver(message("carol", livechange.Inbox))
	if got := received(t, alice); len(got) != 1 || got[0].Change.Kind != livechange.Documents {
		t.Fatalf("alice got %+v", got)
	}
	if got := received(t, bob); len(got) != 0 {
		t.Fatalf("bob got another account's change: %+v", got)
	}
}

func TestEveryTabOfAnAccountHearsEachChange(t *testing.T) {
	hub := NewHub(nil)
	one, _, _ := hub.Subscribe("alice", "")
	two, _, _ := hub.Subscribe("alice", "")
	hub.Deliver(message("alice", livechange.Outputs, livechange.Inbox))
	if len(received(t, one)) != 2 || len(received(t, two)) != 2 {
		t.Fatal("a tab missed a change")
	}
	if hub.Listening() != 2 {
		t.Fatalf("listening %d", hub.Listening())
	}
}

func TestAReconnectResumesWithWhatItMissed(t *testing.T) {
	c := &clock{at: time.Unix(1000, 0)}
	hub := NewHub(c.now)
	first, _, _ := hub.Subscribe("alice", "")
	hub.Deliver(message("alice", livechange.Documents))
	seen := received(t, first)[0].ID
	first.Close()
	c.at = c.at.Add(30 * time.Second)
	hub.Deliver(message("alice", livechange.Outputs, livechange.Notebooks))

	_, replay, resumed := hub.Subscribe("alice", seen)
	if !resumed || len(replay) != 2 || replay[0].Change.Kind != livechange.Outputs || replay[1].Change.Kind != livechange.Notebooks {
		t.Fatalf("resumed %v replay %+v", resumed, replay)
	}
}

func TestAReconnectThatCannotBeVouchedForIsNotResumed(t *testing.T) {
	c := &clock{at: time.Unix(1000, 0)}
	hub := NewHub(c.now)
	sub, _, _ := hub.Subscribe("alice", "")
	hub.Deliver(message("alice", livechange.Documents))
	seen := received(t, sub)[0].ID
	sub.Close()

	other := NewHub(c.now)
	cases := map[string]func() (bool, int){
		"no id": func() (bool, int) { s, r, ok := hub.Subscribe("alice", ""); s.Close(); return ok, len(r) },
		"another process's id": func() (bool, int) {
			s, r, ok := other.Subscribe("alice", seen)
			s.Close()
			return ok, len(r)
		},
		"garbage": func() (bool, int) { s, r, ok := hub.Subscribe("alice", "x-y"); s.Close(); return ok, len(r) },
		"a number from the future": func() (bool, int) {
			s, r, ok := hub.Subscribe("alice", hub.epoch+"-99")
			s.Close()
			return ok, len(r)
		},
	}
	for name, try := range cases {
		if ok, n := try(); ok || n != 0 {
			t.Errorf("%s: resumed %v with %d frames", name, ok, n)
		}
	}

	// Gone longer than linger: the account was forgotten, so its changes
	// since then were never kept.
	c.at = c.at.Add(linger + time.Second)
	hub.Deliver(message("alice", livechange.Inbox))
	if _, replay, resumed := hub.Subscribe("alice", seen); resumed || len(replay) != 0 {
		t.Fatalf("resumed after linger: %v %+v", resumed, replay)
	}
}

func TestAReconnectPastWhatWasKeptIsNotResumed(t *testing.T) {
	hub := NewHub(nil)
	sub, _, _ := hub.Subscribe("alice", "")
	hub.Deliver(message("alice", livechange.Documents))
	seen := received(t, sub)[0].ID
	sub.Close()
	// The one it saw, then one more than is kept after it.
	for range retained + 1 {
		hub.Deliver(message("alice", livechange.Documents))
	}
	if _, _, resumed := hub.Subscribe("alice", seen); resumed {
		t.Fatal("resumed although the oldest missed change was trimmed")
	}
}

func TestAStreamThatStopsReadingIsDroppedAndOthersCarryOn(t *testing.T) {
	hub := NewHub(nil)
	stuck, _, _ := hub.Subscribe("alice", "")
	reading, _, _ := hub.Subscribe("alice", "")
	for i := range subscriberBuffer + 1 {
		hub.Deliver(message("alice", livechange.Documents))
		if i%10 == 0 {
			received(t, reading)
		}
	}
	select {
	case <-stuck.Dropped():
	default:
		t.Fatal("a stream that stopped reading was kept")
	}
	select {
	case <-reading.Dropped():
		t.Fatal("a stream that kept reading was dropped")
	default:
	}
}

func TestAGapEndsEveryStreamAndForgetsWhatWasKept(t *testing.T) {
	hub := NewHub(nil)
	sub, _, _ := hub.Subscribe("alice", "")
	hub.Deliver(message("alice", livechange.Documents))
	seen := received(t, sub)[0].ID
	hub.Gap()
	select {
	case <-sub.Dropped():
	default:
		t.Fatal("a gap left a stream open")
	}
	sub.Close()
	if _, _, resumed := hub.Subscribe("alice", seen); resumed {
		t.Fatal("resumed across a gap")
	}
}
