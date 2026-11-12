package model_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
)

var (
	overloaded = &model.Error{Status: 503, Retryable: true, Message: "overloaded"}
	refused    = &model.Error{Status: 400, Message: "context too long"}
)

func retrying(t *testing.T, m model.Model, p model.RetryPolicy) model.Model {
	t.Helper()
	r, err := model.WithRetry(m, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var quick = model.RetryPolicy{Attempts: 3, Backoff: time.Millisecond, MaxBackoff: 50 * time.Millisecond}

func TestARetryableFailureBeforeAnyTextIsTriedAgain(t *testing.T) {
	m := modeltest.NewScript(modeltest.Reply{Err: overloaded}, modeltest.Reply{Err: overloaded}, modeltest.Reply{Text: "third time"})
	resp, err := retrying(t, m, quick).Generate(context.Background(), model.Request{}, func(model.Chunk) {})
	if err != nil || resp.Message.Content != "third time" || len(m.Requests()) != 3 {
		t.Fatalf("resp %+v, err %v, calls %d", resp, err, len(m.Requests()))
	}
}

func TestAttemptsAreALimit(t *testing.T) {
	m := modeltest.NewScript(modeltest.Reply{Err: overloaded}, modeltest.Reply{Err: overloaded}, modeltest.Reply{Err: overloaded}, modeltest.Reply{Text: "too late"})
	_, err := retrying(t, m, quick).Generate(context.Background(), model.Request{}, func(model.Chunk) {})
	if !errors.Is(err, overloaded) || len(m.Requests()) != 3 {
		t.Fatalf("err %v after %d calls, want the overload after 3", err, len(m.Requests()))
	}
}

func TestARefusedRequestIsNeverSentAgain(t *testing.T) {
	m := modeltest.NewScript(modeltest.Reply{Err: refused}, modeltest.Reply{Text: "never"})
	_, err := retrying(t, m, quick).Generate(context.Background(), model.Request{}, func(model.Chunk) {})
	if !errors.Is(err, refused) || len(m.Requests()) != 1 {
		t.Fatalf("err %v after %d calls, want the refusal after 1", err, len(m.Requests()))
	}
}

func TestAFailureAfterTextHasBeenShownIsFinal(t *testing.T) {
	// A second attempt would write a different answer over text the reader
	// has already seen.
	m := modeltest.NewScript(modeltest.Reply{Text: "Half an", Err: overloaded}, modeltest.Reply{Text: "A different answer"})
	var shown strings.Builder
	_, err := retrying(t, m, quick).Generate(context.Background(), model.Request{}, func(c model.Chunk) { shown.WriteString(c.Text) })
	if !errors.Is(err, overloaded) || len(m.Requests()) != 1 || shown.String() != "Half an" {
		t.Fatalf("err %v, calls %d, shown %q", err, len(m.Requests()), shown.String())
	}
}

func TestTheEndpointsOwnWaitIsHonouredUpToTheCallersCeiling(t *testing.T) {
	wait := &model.Error{Status: 429, Retryable: true, RetryAfter: 30 * time.Millisecond}
	m := modeltest.NewScript(modeltest.Reply{Err: wait}, modeltest.Reply{Text: "ok"})
	began := time.Now()
	if _, err := retrying(t, m, quick).Generate(context.Background(), model.Request{}, func(model.Chunk) {}); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(began); waited < 30*time.Millisecond {
		t.Fatalf("waited %s, less than the 30ms the endpoint asked for", waited)
	}

	// Asked to wait longer than the caller allows, the call fails now
	// instead of parking a goroutine.
	tooLong := &model.Error{Status: 429, Retryable: true, RetryAfter: time.Hour}
	m = modeltest.NewScript(modeltest.Reply{Err: tooLong}, modeltest.Reply{Text: "never"})
	_, err := retrying(t, m, quick).Generate(context.Background(), model.Request{}, func(model.Chunk) {})
	if !errors.Is(err, tooLong) || !strings.Contains(err.Error(), "more than the 50ms this caller allows") {
		t.Fatalf("err = %v", err)
	}
}

func TestCancellingDuringAWaitStopsTheRetry(t *testing.T) {
	m := modeltest.NewScript(modeltest.Reply{Err: overloaded}, modeltest.Reply{Text: "never"})
	slow := model.RetryPolicy{Attempts: 2, Backoff: time.Hour, MaxBackoff: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := retrying(t, m, slow).Generate(ctx, model.Request{}, func(model.Chunk) {})
	if !errors.Is(err, context.DeadlineExceeded) || len(m.Requests()) != 1 {
		t.Fatalf("err %v after %d calls", err, len(m.Requests()))
	}
}

func TestAnIncompletePolicyIsRefused(t *testing.T) {
	m := modeltest.NewScript()
	for _, p := range []model.RetryPolicy{
		{},
		{Attempts: 0, Backoff: time.Second, MaxBackoff: time.Second},
		{Attempts: 2, Backoff: 0, MaxBackoff: time.Second},
		{Attempts: 2, Backoff: time.Second, MaxBackoff: time.Millisecond},
	} {
		if _, err := model.WithRetry(m, p); err == nil {
			t.Errorf("policy %+v was accepted", p)
		}
	}
	if _, err := model.WithRetry(nil, quick); err == nil {
		t.Error("a nil model was accepted")
	}
}
