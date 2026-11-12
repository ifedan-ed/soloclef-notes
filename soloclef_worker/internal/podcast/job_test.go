package podcast

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

func TestAnApprovedBriefBecomesAKeptEpisode(t *testing.T) {
	h := newHarness(t, statusDrafting)
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if h.rows.run.Status != statusReady || h.rows.delivered == nil {
		t.Fatalf("the podcast is %q and delivered=%v", h.rows.run.Status, h.rows.delivered != nil)
	}
	transcript := h.transcript(t)
	// Three beats of two lines; the line given to slot 9 was dropped.
	if len(transcript.Turns) != 6 {
		t.Fatalf("%d lines, want 6", len(transcript.Turns))
	}
	for _, turn := range transcript.Turns {
		if turn.Speaker == 9 {
			t.Fatal("a line for a slot the brief does not have was kept")
		}
	}
	// Each line voiced once: the draft voiced them ahead and the render
	// read them from the cache.
	if voiced := h.voicer.count(); voiced != 6 {
		t.Fatalf("%d lines were voiced, want each of the 6 once", voiced)
	}
	if h.rows.delivered.Seconds != 6 {
		t.Fatalf("the episode measured %v", h.rows.delivered.Seconds)
	}
	if !strings.Contains(h.rows.delivered.Markdown, "**Ada:** Line 2 from the host.") {
		t.Fatalf("the episode's text does not name its speakers:\n%s", h.rows.delivered.Markdown)
	}
	if len(h.indexer.documents) != 1 || h.indexer.documents[0] != 77 {
		t.Fatalf("the episode's document was not queued for indexing: %v", h.indexer.documents)
	}
	h.workdirIsEmpty(t)
}

// A speaker's own delivery note is the style; the deployment's is the
// style of a speaker without one.
func TestEachSpeakerIsVoicedInItsOwnVoiceAndStyle(t *testing.T) {
	h := newHarness(t, statusDrafting)
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	for _, line := range h.voicer.lines {
		switch {
		case strings.Contains(line.Text, "host"):
			if line.Voice != "marin" || line.Instructions != "Bright." {
				t.Fatalf("the host was voiced as %+v", line)
			}
		case strings.Contains(line.Text, "guest"):
			if line.Voice != "cedar" || line.Instructions != "Calm." {
				t.Fatalf("the guest was voiced as %+v", line)
			}
		}
	}
}

// A cancel made while the transcript is being written stops the drafting
// at the next beat, and the cancel stays.
func TestACancelDuringTheDraftIsNotOverwritten(t *testing.T) {
	h := newHarness(t, statusDrafting)
	h.model.onCall = func(call int) {
		if call == 2 { // the first beat
			h.rows.set("cancelled")
		}
	}
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if h.rows.run.Status != "cancelled" || h.rows.run.Transcript != nil || h.rows.delivered != nil {
		t.Fatalf("the cancel was overwritten: status %q, transcript %s", h.rows.run.Status, h.rows.run.Transcript)
	}
	if h.model.calls > 2 {
		t.Fatalf("drafting went on for %d calls after the cancel", h.model.calls-2)
	}
	h.workdirIsEmpty(t)
}

// A cancel made while the lines are being voiced stops the render, and no
// episode is kept.
func TestACancelDuringTheRenderIsNotOverwritten(t *testing.T) {
	h := newHarness(t, statusRendering)
	h.rows.run.Transcript = []byte(`{"turns":[{"speaker":0,"text":"One."},{"speaker":1,"text":"Two."},{"speaker":0,"text":"Three."},{"speaker":1,"text":"Four."}]}`)
	h.job.Config.Concurrency = 1
	h.voicer.before = func(count int) {
		if count == 1 {
			h.rows.set("cancelled")
		}
	}
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if h.rows.run.Status != "cancelled" || h.rows.delivered != nil {
		t.Fatalf("the cancel was overwritten: status %q, delivered %v", h.rows.run.Status, h.rows.delivered != nil)
	}
	if voiced := h.voicer.count(); voiced > 2 {
		t.Fatalf("%d lines were voiced after the cancel", voiced-1)
	}
	h.workdirIsEmpty(t)
}

// A podcast delivered again after a restart finds its transcript stored and
// only renders: nothing is drafted twice.
func TestAJobDeliveredAgainAfterTheDraftOnlyRenders(t *testing.T) {
	h := newHarness(t, statusRendering)
	h.rows.run.Transcript = []byte(`{"turns":[{"speaker":0,"text":"Only line."}]}`)
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if h.model.calls != 0 {
		t.Fatalf("the model was asked %d times for a podcast already drafted", h.model.calls)
	}
	if h.rows.run.Status != statusReady {
		t.Fatalf("status %q", h.rows.run.Status)
	}
}

func TestAPodcastNoLongerBeingMadeIsLeftAlone(t *testing.T) {
	for _, status := range []string{"cancelled", "ready", "awaiting_brief", "failed"} {
		h := newHarness(t, status)
		if err := h.run(t); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if h.rows.run.Status != status || h.model.calls != 0 || h.voicer.count() != 0 {
			t.Fatalf("%s: the podcast was touched", status)
		}
	}
}

// The person is told what to do; the provider's words go to the log.
func TestAFailedDraftTellsThePersonInPlainWords(t *testing.T) {
	h := newHarness(t, statusDrafting)
	h.model.refuse = true
	if err := h.run(t); err == nil {
		t.Fatal("a refused draft was reported as done")
	}
	if h.rows.run.Status != "failed" || h.rows.reason != draftFailed {
		t.Fatalf("the podcast is %q with %q", h.rows.run.Status, h.rows.reason)
	}
	h.workdirIsEmpty(t)
}

func TestAVoiceFromAnotherServiceFailsWithAReasonThePersonCanActOn(t *testing.T) {
	h := newHarness(t, statusRendering)
	h.rows.run.Spec = []byte(strings.Replace(twoSpeakerBrief, "openai:cedar", "kokoro:am_adam", 1))
	h.rows.run.Transcript = []byte(`{"turns":[{"speaker":1,"text":"Hello."}]}`)
	if err := h.run(t); err == nil {
		t.Fatal("a voice from another service was spoken")
	}
	if !strings.Contains(h.rows.reason, "kokoro:am_adam") {
		t.Fatalf("the reason does not name the voice: %q", h.rows.reason)
	}
}

// A worker that is stopping hands the podcast back rather than failing it:
// the next delivery carries on from the lines already voiced.
func TestAStoppingWorkerHandsThePodcastBack(t *testing.T) {
	h := newHarness(t, statusRendering)
	h.rows.run.Transcript = []byte(`{"turns":[{"speaker":0,"text":"One."}]}`)
	ctx, stop := context.WithCancel(context.Background())
	h.voicer.before = func(int) { stop() }
	h.voicer.fail = context.Canceled
	err := h.job.Run(ctx, &celery.Message{Task: Task, Args: []any{float64(5)}})
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("got %v, want the message handed back", err)
	}
	if h.rows.run.Status != statusRendering {
		t.Fatalf("a podcast cut off by a restart was marked %q", h.rows.run.Status)
	}
}

func TestTheMessageMustNameOnePodcast(t *testing.T) {
	h := newHarness(t, statusDrafting)
	for _, args := range [][]any{nil, {"5"}, {float64(5), float64(2)}, {float64(0)}, {float64(1.5)}} {
		if err := h.job.Run(context.Background(), &celery.Message{Task: Task, Args: args}); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// A message delivered twice is two jobs on one podcast. The one that loses
// the race to store its transcript stops, and leaves the lines the winner
// is rendering from where they are.
func TestTheLoserOfADuplicateDeliveryLeavesTheWinnersLines(t *testing.T) {
	h := newHarness(t, statusDrafting)
	h.model.onCall = func(call int) {
		if call == 4 { // the last beat: the other job stores its transcript first
			h.rows.mu.Lock()
			h.rows.run.Status = statusRendering
			h.rows.mu.Unlock()
		}
	}
	h.job.Config.StatusInterval = time.Hour // the race is decided at the locked write
	if err := h.run(t); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(h.workdir, "5", "lines"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("the lines the other job is rendering from were removed: %v, %v", entries, err)
	}
	if h.rows.delivered != nil {
		t.Fatal("the job that lost the race delivered an episode")
	}
}
