package studiofailures

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// Every code an artifact job can be closed with has the owner's sentence:
// the runner's own and the specialist's.
func TestEveryArtifactFailureCodeHasASentence(t *testing.T) {
	for _, code := range []string{ModelUnavailable, Timeout, StepLimit, GenerationFailed, NothingMade, Interrupted,
		deliverables.FailureSandboxBusy, deliverables.FailureNoSandbox, deliverables.FailureSandbox,
		deliverables.FailurePreview, deliverables.FailureDrawing, deliverables.FailureStorage} {
		if ForArtifact(code) == "" {
			t.Errorf("%s has no sentence", code)
		}
	}
	if ForArtifact("no_such_code") != "" || ForArtifact(Cancelled) != "" {
		t.Error("an unknown code, or a cancellation, was given a failure sentence")
	}
}

// The video codes are the worker's (videojob/failure.go), spelled here
// because this module cannot import the worker's.
func TestEveryVideoFailureCodeHasASentence(t *testing.T) {
	for _, code := range []string{"duration_limit", "quota_exceeded", "generation_failed", "render_failed", "verification_failed", "interrupted"} {
		if ForVideo(code) == "" {
			t.Errorf("%s has no sentence", code)
		}
	}
	if ForVideo(NothingMade) != "" {
		t.Error("an artifact's code was given a video sentence")
	}
}
