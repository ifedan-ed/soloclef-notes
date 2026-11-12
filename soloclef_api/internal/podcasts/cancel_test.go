package podcasts

import (
	"net/http"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

const cancelPath = "/api/v1/podcasts/41/cancel"

// A cancel against every state a podcast can be in. Nothing is billed any
// more, so a cancel is a mark on the row and nothing else: no ledger, no
// queue, no delete.
func TestCancelFromEveryState(t *testing.T) {
	for _, c := range []struct {
		name    string
		from    string
		episode bool
		status  int
		to      string
		message string
	}{
		{"before a brief is proposed", statusPending, false, 200, statusCancelled, ""},
		{"at the brief gate", statusAwaitingBrief, false, 200, statusCancelled, ""},
		{"while the transcript is drafted", statusDrafting, false, 200, statusCancelled, ""},
		{"at the old review gate", statusAwaitingReview, false, 200, statusCancelled, ""},
		{"while the audio is rendered", statusRendering, false, 200, statusCancelled, ""},
		// Asked twice, it is done once and answered as done.
		{"already cancelled", statusCancelled, false, 200, statusCancelled, ""},
		{"after it failed", statusFailed, false, 409, statusFailed, "failed -> cancelled is not allowed"},
		// No action may destroy audio a listener could keep. A new take of
		// a finished episode is backed out of with a revert.
		{"a finished episode", statusReady, true, 409, statusReady, episodeExists},
		{"a new take of a finished episode, at the gate", statusAwaitingBrief, true, 409, statusAwaitingBrief, episodeExists},
		{"a new take of a finished episode, drafting", statusDrafting, true, 409, statusDrafting, episodeExists},
		{"a new take, already cancelled somehow", statusCancelled, true, 409, statusCancelled, episodeExists},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(podcast(41, func(row *PodcastOfOwnerRow) {
				row.Status = c.from
				row.PodcastTranscript = text(goodTranscript)
				if c.episode {
					row.ArtifactID = pointer(episode)
				}
			}))
			got := ask(t, f, principal.MethodSession, http.MethodPost, cancelPath)
			if got.status != c.status || got.message() != c.message {
				t.Fatalf("%d %q, want %d %q", got.status, got.message(), c.status, c.message)
			}
			row := f.podcasts[41]
			if row.Status != c.to {
				t.Errorf("left %q, want %q", row.Status, c.to)
			}
			// The brief and the transcript drafted so far stay on the row.
			if string(row.Spec) != goodBrief || string(row.PodcastTranscript) != goodTranscript {
				t.Error("a cancel changed more than the status")
			}
			if len(f.enqueued) != 0 {
				t.Errorf("a cancel queued %v", f.enqueued)
			}
			wantMoves := 0
			if c.status == http.StatusOK && c.from != statusCancelled {
				wantMoves = 1
			}
			if f.moves != wantMoves {
				t.Errorf("%d statements changed a status, want %d", f.moves, wantMoves)
			}
			if c.status == http.StatusOK && (got.body["status"] != statusCancelled || got.body["id"] != 41.0 || got.body["has_audio"] != false) {
				t.Errorf("the answer is %v", got.body)
			}
		})
	}
}

// The render finishes in the instant the button is pressed. The cancel was
// decided on `rendering`; it must not overwrite `ready`.
func TestACancelThatLosesToTheRenderIsAConflict(t *testing.T) {
	f := newFake(podcast(41, func(row *PodcastOfOwnerRow) { row.Status = statusRendering }))
	f.meanwhile = func(f *fake) {
		row := f.podcasts[41]
		row.Status, row.ArtifactID = statusReady, pointer(episode)
		f.podcasts[41] = row
	}
	got := ask(t, f, principal.MethodSession, http.MethodPost, cancelPath)
	if got.status != http.StatusConflict || got.message() != changedMeanwhile {
		t.Errorf("%d %q", got.status, got.message())
	}
	if f.podcasts[41].Status != statusReady {
		t.Errorf("a finished episode was left %q", f.podcasts[41].Status)
	}
}

// `cancelled` has no exits, which is what stops the job: a worker that looks
// at the row before it writes finds it may not.
func TestNothingLeavesCancelled(t *testing.T) {
	if exits := allowed[statusCancelled]; len(exits) != 0 {
		t.Errorf("cancelled can move to %v", exits)
	}
	for _, path := range []string{"/brief/approve", "/transcript/regenerate", "/regenerate/revert"} {
		f := newFake(podcast(41, func(row *PodcastOfOwnerRow) { row.Status = statusCancelled }))
		got := ask(t, f, principal.MethodSession, http.MethodPost, "/api/v1/podcasts/41"+path)
		if got.status != http.StatusConflict || f.podcasts[41].Status != statusCancelled || len(f.enqueued) != 0 {
			t.Errorf("%s of a cancelled podcast: %d, left %q", path, got.status, f.podcasts[41].Status)
		}
	}
}
