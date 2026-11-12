// Package studiogenerate starts a notebook's studio outputs and offers the
// ideas its dialogs show:
//
//	POST /api/v1/notebooks/{notebook_id}/studio/generate      start an output
//	GET  /api/v1/notebooks/{notebook_id}/studio/suggestions   ideas for a dialog's topic box
//
// Listing, renaming and deleting outputs are internal/studiooutputs', and
// the answer to a generate is that package's own rendering of the new row.
//
// # How an output is made
//
// Exactly as the chat makes one, with the pieces the chat built
// (chat.Parts): nothing here is a second way of making an artifact.
//
//   - Audio opens a podcast through the specialist's own opener and then
//     approves its brief with the podcast routes' own transition
//     (podcasts.StartDraft): the dialog has already asked what the brief
//     card would.
//   - Video queues the render job through the specialist's own queuer.
//   - Everything else (flashcards, a quiz, a mind map, an infographic, a
//     report, slides, a data table) is the artifact specialist, run here
//     with nobody watching (runner.go): the task is written from the
//     dialog's options and the sources' text, and a deliverable_jobs row
//     of kind `artifact` carries its progress to the panel.
//
// Each generation has a hidden thread of its own, because the sandbox and an
// artifact's attribution are keyed by thread.
//
// # What it does better than the Python
//
//   - **Every statement names the owner.** A source id of another notebook
//     or account refuses the request; a document of another account is not
//     read even if a source row named it.
//   - **A document being deleted is not generated from.**
//   - **An artifact run needs no worker and no queue message**: it starts at
//     once, heartbeats, stops when the person deletes or cancels the output,
//     and is failed by a sweep if its process dies, so the panel never spins
//     on a job nothing is making.
//   - **The request is refused whole and at once** for an option outside
//     its vocabulary or a field the kind does not have; nothing is guessed.
//   - **A repeated click is one output**, by the request id, as before, and
//     now also for a click that lost a race.
//   - **A source list that changed drops the cached ideas in the same
//     statement that stores the new ones**, not by a read and a write.
package studiogenerate

import "net/http"

// The paths.
const (
	GeneratePath    = "/api/v1/notebooks/{notebook_id}/studio/generate"
	SuggestionsPath = "/api/v1/notebooks/{notebook_id}/studio/suggestions"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodPost + " " + GeneratePath,
		http.MethodGet + " " + SuggestionsPath,
	}
}

// What a refusal says.
const (
	notebookAbsent   = "Notebook not found"
	sourceAbsent     = "Source not found"
	noSources        = "Choose at least one source to generate from"
	notEnabled       = "Generation is not enabled on this server."
	modelUnavailable = "The model could not be reached right now. Please try again."
	alreadyStarting  = "This generation is already starting."
)
