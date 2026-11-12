package process

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTogetherRunsUntilOneStopsAndThenStopsTheRest(t *testing.T) {
	stopped := make(chan string, 2)
	failed := errors.New("the first one failed")
	err := Together(context.Background(),
		Service{Name: "first", Run: func(context.Context) error {
			stopped <- "first"
			return failed
		}},
		Service{Name: "second", Run: func(ctx context.Context) error {
			<-ctx.Done()
			stopped <- "second"
			return nil
		}},
	)
	if !errors.Is(err, failed) {
		t.Fatalf("together returned %v, want %v", err, failed)
	}
	if got := <-stopped; got != "first" {
		t.Errorf("the failing service was %q", got)
	}
	select {
	case got := <-stopped:
		if got != "second" {
			t.Errorf("the second service reported %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the other service was never stopped")
	}
}

func TestTogetherIsCleanWhenTheContextEnds(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	err := Together(ctx, Service{Name: "quiet", Run: func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}})
	if err != nil {
		t.Errorf("together returned %v, want nil", err)
	}
}

func TestTogetherKeepsEveryFailure(t *testing.T) {
	first := errors.New("one")
	second := errors.New("two")
	err := Together(context.Background(),
		Service{Name: "first", Run: func(context.Context) error { return first }},
		Service{Name: "second", Run: func(ctx context.Context) error {
			<-ctx.Done()
			return second
		}},
	)
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Errorf("together returned %v, want both failures", err)
	}
}
