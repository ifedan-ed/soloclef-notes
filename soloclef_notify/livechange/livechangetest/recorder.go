// Package livechangetest is a Publisher for tests: it keeps what was
// published so a test can say which changes a write owes.
package livechangetest

import (
	"context"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// Published is one call to Publish.
type Published struct {
	UserID  string
	Changes []livechange.Change
}

// Recorder is livechange.Publisher, remembering every call.
type Recorder struct {
	mu    sync.Mutex
	calls []Published
}

// Publish is livechange.Publisher.
func (r *Recorder) Publish(_ context.Context, userID string, changes ...livechange.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, Published{UserID: userID, Changes: append([]livechange.Change(nil), changes...)})
}

// Calls is every publish so far, in order.
func (r *Recorder) Calls() []Published {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Published(nil), r.calls...)
}

// Kinds is every change's kind for one account, in order: what most tests
// assert on.
func (r *Recorder) Kinds(userID string) []livechange.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kinds []livechange.Kind
	for _, call := range r.calls {
		if call.UserID != userID {
			continue
		}
		for _, change := range call.Changes {
			kinds = append(kinds, change.Kind)
		}
	}
	return kinds
}
