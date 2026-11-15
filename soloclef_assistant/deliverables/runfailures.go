package deliverables

import (
	"sync"
)

// # Why an unattended run made nothing
//
// A chat's specialist reports a failure to the main model, which tells the
// person in its own words. A Studio output or an automation's step has
// nobody to tell: what the person reads is the run's row and its
// notification. Those must be this server's own sentences, keyed by a
// failure code the server chose, and never the specialist model's summary:
// a model that has just been refused a sandbox can still write "Saved a
// 15-slide deck…" before admitting that nothing was stored (Studio job 33,
// 14 Nov 2026).
//
// So the tools note each failure the person could be told about, as a code,
// where it happens (Failures, below). When a run ends with nothing saved,
// the runner reads the last one noted and closes the job with its code and
// FailureSentence; the model's answer goes to the log. A failure the model
// recovered from (a second try that saved) is never read: the saved
// artifact is the outcome.

// The failure codes a run's tools note. Each has its sentence in
// failureSentences; the deliverable_jobs row stores the code.
const (
	// FailureSandboxBusy is an account whose sandboxes all stayed in use
	// for the whole of the run's wait.
	FailureSandboxBusy = "sandbox_busy"
	// FailureNoSandbox is a deployment with no sandbox service.
	FailureNoSandbox = "sandbox_unavailable"
	// FailureSandbox is the sandbox failing, or its service not answering.
	FailureSandbox = "sandbox_failed"
	// FailurePreview is LibreOffice failing to draw a document's preview.
	FailurePreview = "preview_failed"
	// FailureDrawing is this application's own renderer writing a deck
	// that does not read back: a fault here, never the model's.
	FailureDrawing = "drawing_failed"
	// FailureStorage is a file or its rows that could not be stored.
	FailureStorage = "storage_failed"
)

// failureSentences is what the person is told for each code: one sentence
// or two, saying what happened and what, if anything, they can do.
var failureSentences = map[string]string{
	FailureSandboxBusy: "All of your code sandboxes stayed in use by your other conversations and outputs, so this could not be built. Try again once they have finished.",
	FailureNoSandbox:   noSandboxSentence,
	FailureSandbox:     "The code sandbox failed while this was being built. Try again in a few minutes.",
	FailurePreview:     "The preview could not be drawn, so nothing was saved. Try again.",
	FailureDrawing:     "It could not be drawn because of a fault in this application, so nothing was saved.",
	FailureStorage:     "It was made but could not be stored because of a storage error. Try again.",
}

// FailureSentence is the person's sentence for a code the tools note, and
// whether the code is one of theirs.
func FailureSentence(code string) (string, bool) {
	sentence, known := failureSentences[code]
	return sentence, known
}

// Failure is one failure a tool noted: its code, and the cause for the log
// and the row's internal_error (never shown to the person).
type Failure struct {
	Code  string
	Cause string
}

// Failures collects what a run's tools noted. A run nobody watches is given
// one (Specialist.Failures); a chat's specialist has none, and noting is
// then nothing. It is safe for concurrent use.
type Failures struct {
	mu   sync.Mutex
	last *Failure
}

// note records a failure; the last one noted is the run's. A nil
// collector notes nothing.
func (f *Failures) note(code string, cause error) {
	if f == nil {
		return
	}
	noted := Failure{Code: code}
	if cause != nil {
		noted.Cause = cause.Error()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = &noted
}

// Last is the last failure noted, if any.
func (f *Failures) Last() (Failure, bool) {
	if f == nil {
		return Failure{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		return Failure{}, false
	}
	return *f.last, true
}
