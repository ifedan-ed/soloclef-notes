package studiooutputs

import "testing"

// A podcast a notebook's chat opened waits for the person to approve its
// brief: the panel must be able to say so, not just "Queued".
func TestPodcastAtItsBriefSaysItWaitsOnThePerson(t *testing.T) {
	out := outputOf(OutputsRow{Kind: "audio", PodcastStatus: []byte(`"awaiting_brief"`)})
	if out.Status != "queued" || out.Phase == nil || *out.Phase != "awaiting_brief" {
		t.Fatalf("awaiting_brief listed as %q, phase %v", out.Status, out.Phase)
	}
	pending := outputOf(OutputsRow{Kind: "audio", PodcastStatus: []byte(`"pending"`)})
	if pending.Status != "queued" || pending.Phase != nil {
		t.Fatalf("pending listed as %q, phase %v", pending.Status, pending.Phase)
	}
}

// A failed output carries the sentence its notification gave, so the row
// can say why and not only "Failed".
func TestAFailedOutputSaysWhy(t *testing.T) {
	text := func(s string) *string { return &s }
	reason := func(out output) string {
		if out.FailureReason == nil {
			return ""
		}
		return *out.FailureReason
	}
	for name, c := range map[string]struct {
		row  OutputsRow
		want string
	}{
		"an artifact job, by its code": {OutputsRow{Kind: "report", JobStatus: []byte(`"failed"`), JobFailure: text("timeout")},
			"It took longer than this server allows. Try again."},
		"a specialist's code": {OutputsRow{Kind: "slides", JobStatus: []byte(`"failed"`), JobFailure: text("sandbox_busy")},
			"All of your code sandboxes stayed in use by your other conversations and outputs, so this could not be built. Try again once they have finished."},
		"a video, by the worker's code": {OutputsRow{Kind: "video", JobStatus: []byte(`"failed"`), JobFailure: text("render_failed")},
			"The video could not be rendered. Try again."},
		"a podcast, in the worker's own words": {OutputsRow{Kind: "audio", PodcastStatus: []byte(`"failed"`), PodcastError: text("The podcast could not be made. Try again. ")},
			"The podcast could not be made. Try again."},
		"a code with no sentence":  {OutputsRow{Kind: "report", JobStatus: []byte(`"failed"`), JobFailure: text("generation_failed")}, ""},
		"a run that is gone":       {OutputsRow{Kind: "report"}, ""},
		"a ready one says nothing": {OutputsRow{Kind: "report", JobStatus: []byte(`"ready"`), JobFailure: text("timeout")}, ""},
	} {
		if got := reason(outputOf(c.row)); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
