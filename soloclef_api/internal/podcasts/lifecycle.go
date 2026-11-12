package podcasts

import (
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// The members of PodcastStatus (app/podcasts/persistence/enums), which are
// also the labels of the `podcast_status` enum in Postgres.
const (
	statusPending        = "pending"
	statusAwaitingBrief  = "awaiting_brief"
	statusDrafting       = "drafting"
	statusAwaitingReview = "awaiting_review"
	statusRendering      = "rendering"
	statusReady          = "ready"
	statusFailed         = "failed"
	statusCancelled      = "cancelled"
)

// allowed is the lifecycle: the only status changes there are
// (app/podcasts/service.py, _ALLOWED -- the two tiers and the tasks all move
// the same rows, so this has to be that table). `failed` and `cancelled` have
// no exits, and `awaiting_review` is entered by nothing any more and still
// has its own, so an old row is not stranded.
var allowed = map[string][]string{
	statusPending:        {statusAwaitingBrief, statusFailed, statusCancelled},
	statusAwaitingBrief:  {statusDrafting, statusReady, statusFailed, statusCancelled},
	statusDrafting:       {statusRendering, statusReady, statusFailed, statusCancelled},
	statusAwaitingReview: {statusAwaitingBrief, statusFailed, statusCancelled},
	statusRendering:      {statusReady, statusFailed, statusCancelled},
	statusReady:          {statusAwaitingBrief},
	statusFailed:         {},
	statusCancelled:      {},
}

// refusal is an answer a route gives on purpose: a status and a sentence. It
// is an error so that returning one from a write rolls the transaction back.
type refusal struct {
	status  int
	message string
}

func (r *refusal) Error() string { return r.message }

// invalid is a request whose body is well-formed and wrong: the failures, each
// at the field it is about. It is an error for the reason refusal is one.
type invalid struct {
	failures []errorenvelope.Failure
}

func (i *invalid) Error() string { return "the request does not validate" }

func conflict(message string) *refusal {
	return &refusal{status: http.StatusConflict, message: message}
}

func unmet(message string) *refusal {
	return &refusal{status: http.StatusUnprocessableEntity, message: message}
}

// transition is the target if the lifecycle allows it from where the podcast
// is, and a 409 naming both states if not.
func transition(podcast *Stored, target string) *refusal {
	if !oneOf(target, allowed[podcast.Status]) {
		return conflict(podcast.Status + " -> " + target + " is not allowed")
	}
	return nil
}

// usableBrief refuses a podcast that has no brief, or has one that cannot be
// read: both are about to be drafted from, and neither can be.
func usableBrief(podcast *Stored, absent string) *refusal {
	brief, readable := readSpec(podcast.Spec)
	switch {
	case !readable:
		return unmet(briefUnreadable)
	case brief == nil:
		return unmet(absent)
	}
	return nil
}

// An action is one of the three transitions: what has to hold before the
// podcast moves, and where it moves to.
type action func(podcast *Stored) (target string, refused *refusal)

// approve starts the transcript draft: the brief has been reviewed, so there
// has to be one, and the podcast has to be waiting at the brief gate.
func approve(podcast *Stored) (string, *refusal) {
	if refused := transition(podcast, statusDrafting); refused != nil {
		return "", refused
	}
	return statusDrafting, usableBrief(podcast, noBriefToDraft)
}

// regenerate reopens the brief gate for a fresh take of a finished episode.
// The saved brief is the starting point, so an episode from before briefs
// existed cannot be regenerated: the gate would have nothing to show.
//
// Only a finished take can be reopened. From `pending` the same target is a
// legal transition, but there it means attaching a first brief.
func regenerate(podcast *Stored) (string, *refusal) {
	if podcast.Status != statusReady && podcast.Status != statusAwaitingReview {
		return "", conflict(nothingToRedo + podcast.Status)
	}
	return statusAwaitingBrief, usableBrief(podcast, noBriefToRedo)
}

// revert backs out of a regeneration, to the episode that is still stored.
// A podcast that never finished has nothing to go back to.
func revert(podcast *Stored) (string, *refusal) {
	if podcast.ArtifactID == nil {
		return "", conflict(noEpisode)
	}
	return statusReady, transition(podcast, statusReady)
}

// cancel abandons a podcast that has produced nothing a listener could keep.
//
// No action may destroy playable audio. Once an episode exists, backing out
// of a new take is a revert, which keeps it; so a podcast with an episode is
// refused here whatever its status. One that is already cancelled is
// answered as done: that is what was asked for, and a second press of the
// button is not an error.
func cancel(podcast *Stored) (string, *refusal) {
	if podcast.ArtifactID != nil {
		return "", conflict(episodeExists)
	}
	if podcast.Status == statusCancelled {
		return statusCancelled, nil
	}
	return statusCancelled, transition(podcast, statusCancelled)
}
