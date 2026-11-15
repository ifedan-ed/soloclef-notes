package inflight

import (
	"context"
	"testing"
	"time"
)

func TestAnIdleCounterAnswersAtOnce(t *testing.T) {
	var c Counter
	select {
	case <-c.Idle():
	default:
		t.Fatal("a counter with nothing in flight should be idle")
	}
	c.Wait()
}

func TestIdleIsClosedWhenTheLastPieceEnds(t *testing.T) {
	var c Counter
	c.Begin()
	c.Begin()
	idle := c.Idle()
	c.End()
	select {
	case <-idle:
		t.Fatal("idle with one piece still in flight")
	default:
	}
	c.End()
	select {
	case <-idle:
	case <-time.After(time.Second):
		t.Fatal("not idle after the last piece ended")
	}
}

func TestWorkThatBeginsAgainOpensANewWait(t *testing.T) {
	var c Counter
	c.Begin()
	c.End()
	c.Begin()
	select {
	case <-c.Idle():
		t.Fatal("idle while a new piece is in flight")
	default:
	}
	c.End()
	c.Wait()
}

func TestEndWithoutBeginPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("an unmatched End should panic")
		}
	}()
	var c Counter
	c.End()
}

func TestAllIdleWaitsForEveryCounterAtOnce(t *testing.T) {
	var chat, studio Counter
	chat.Begin()
	studio.Begin()
	result := make(chan bool, 1)
	go func() { result <- AllIdle(context.Background(), &chat, &studio) }()

	// The chat finishes, then begins again before the studio finishes: the
	// wait must not end on the studio's finish alone.
	chat.End()
	chat.Begin()
	studio.End()
	select {
	case <-result:
		t.Fatal("AllIdle ended while the chat had work in flight")
	case <-time.After(50 * time.Millisecond):
	}
	chat.End()
	select {
	case ok := <-result:
		if !ok {
			t.Error("AllIdle reported a timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("AllIdle did not end once both were idle")
	}
}

func TestAllIdleGivesUpAtItsBound(t *testing.T) {
	var c Counter
	c.Begin()
	defer c.End()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if AllIdle(ctx, &c) {
		t.Error("AllIdle reported idle with work in flight")
	}
}
