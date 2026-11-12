package model

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RetryPolicy says how WithRetry behaves. Every field must be set: a retry
// loop whose numbers come from a default nobody chose is how a turn ends up
// waiting minutes with no one knowing why.
type RetryPolicy struct {
	// Attempts is the total number of calls, the first included. 1 means no
	// retry.
	Attempts int

	// Backoff is the wait before the second attempt; it doubles for each
	// attempt after that.
	Backoff time.Duration

	// MaxBackoff caps one wait, including a wait the endpoint asked for. An
	// endpoint that says "retry after an hour" gets a failed turn, not a
	// goroutine parked for an hour.
	MaxBackoff time.Duration
}

// WithRetry wraps a Model so that a retryable failure is tried again.
//
// It retries only when two things hold. The error is an *Error marked
// Retryable, so a refused request is never resent. And nothing has been
// streamed yet: once a reader has seen text, a second attempt would write a
// different answer over it, so a failure after the first chunk is final.
func WithRetry(next Model, policy RetryPolicy) (Model, error) {
	if next == nil {
		return nil, errors.New("model: WithRetry needs a model to wrap")
	}
	if policy.Attempts < 1 || policy.Backoff <= 0 || policy.MaxBackoff < policy.Backoff {
		return nil, fmt.Errorf("model: retry policy %+v is incomplete: Attempts must be at least 1, Backoff positive, MaxBackoff at least Backoff", policy)
	}
	return &retrying{next: next, policy: policy}, nil
}

type retrying struct {
	next   Model
	policy RetryPolicy
}

func (r *retrying) Generate(ctx context.Context, req Request, stream func(Chunk)) (Response, error) {
	wait := r.policy.Backoff
	for attempt := 1; ; attempt++ {
		streamed := false
		resp, err := r.next.Generate(ctx, req, func(c Chunk) {
			streamed = true
			stream(c)
		})
		if err == nil {
			return resp, nil
		}

		var modelErr *Error
		if !errors.As(err, &modelErr) || !modelErr.Retryable || streamed || attempt == r.policy.Attempts {
			return Response{}, err
		}

		// The endpoint's own figure wins over ours when it gave one: it knows
		// when its rate window reopens and we are only guessing.
		pause := wait
		if modelErr.RetryAfter > 0 {
			pause = modelErr.RetryAfter
		}
		if pause > r.policy.MaxBackoff {
			return Response{}, fmt.Errorf("model asked to wait %s, more than the %s this caller allows: %w", pause, r.policy.MaxBackoff, err)
		}

		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Response{}, ctx.Err()
		case <-timer.C:
		}
		if wait *= 2; wait > r.policy.MaxBackoff {
			wait = r.policy.MaxBackoff
		}
	}
}
