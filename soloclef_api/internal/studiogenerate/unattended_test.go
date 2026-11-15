package studiogenerate

import (
	"io"
	"log/slog"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// A run that saved nothing and met no failure its tools could name is
// nothing_made, with the server's detail: the model's answer is not kept on
// the row. (A noted failure's code is the database test's: the collector is
// filled only by the specialist's tools.)
func TestNothingMadeIsTheServersOwnReason(t *testing.T) {
	r := &Runner{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	const answer = "Saved a 15-slide deck."
	for name, failures := range map[string]*deliverables.Failures{"an empty collector": {}, "none at all": nil} {
		end := r.nothingMade(Job{ID: 1}, failures, answer)
		if end.status != DeliverableJobStatusFailed || end.code != codeNothingMade || end.detail == answer || end.detail == "" {
			t.Errorf("%s: %+v", name, end)
		}
	}
}

// Every code a job can be closed with has the owner's sentence: the
// runner's own and the specialist's.
func TestEveryFailureCodeHasASentence(t *testing.T) {
	for _, code := range []string{codeModel, codeTimeout, codeStepLimit, codeNothingMade, codeInterrupted,
		deliverables.FailureSandboxBusy, deliverables.FailureNoSandbox, deliverables.FailureSandbox,
		deliverables.FailurePreview, deliverables.FailureDrawing, deliverables.FailureStorage} {
		if reasonFor(code) == "" {
			t.Errorf("%s has no sentence", code)
		}
	}
	if reasonFor("no_such_code") != "" {
		t.Error("an unknown code was given a sentence")
	}
}
