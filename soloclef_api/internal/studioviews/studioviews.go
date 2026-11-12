// Package studioviews serves what the Studio's viewers show around a
// finished output:
//
//	GET  /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/view          its prompt and sources
//	GET  /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/report-view   a report's reading view
//	POST …/report-suggestions/{suggestion_id}/add                                 make the output a suggestion proposes
//	POST …/report-suggestions/{suggestion_id}/dismiss                             put a suggestion away
//
// A report's reading view carries a few suggestions, each a studio output
// proposed under one section of the report. They are written once per
// version of the report by the account's quick model, through the model
// proxy, and kept in the report artifact's metadata (entry.go). Adding one
// starts an ordinary studio generation (studiogenerate.StartFromReport), so
// it also shows in the Studio list, and the suggestion keeps the new
// output's id so the viewer can show its progress in place.
//
// # What it does better than the Python
//
//   - **Every statement names the owner**, joins included; the Python read
//     the artifact by workspace.
//   - **A finished output is one statement** (ReadyOutput), where the Python
//     read the output, then the whole list of outputs for its status, then
//     the artifact, then its files and its document.
//   - **Only suggestions the deployment can make are proposed.** The Python
//     offered audio alone without a sandbox; flashcards, a quiz, a mind map
//     and an infographic need none, and are now offered too.
//   - **One odd stored suggestion does not fail the view**: an item with no
//     id, kind or title is left out, where the Python raised on it.
//   - **A change to a suggestion is read and written under the artifact's
//     row lock in one transaction**, by owner; the Python locked the row by
//     id alone.
//   - **The view never waits on a model longer than it says**: the call is
//     bounded (ModelTimeout an attempt, two attempts), and a failure is the
//     view without suggestions, said as `unavailable`, never a 500.
package studioviews

import "net/http"

// The paths.
const (
	ViewPath       = "/api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/view"
	ReportViewPath = "/api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/report-view"
	AddPath        = "/api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/report-suggestions/{suggestion_id}/add"
	DismissPath    = "/api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/report-suggestions/{suggestion_id}/dismiss"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + ViewPath,
		http.MethodGet + " " + ReportViewPath,
		http.MethodPost + " " + AddPath,
		http.MethodPost + " " + DismissPath,
	}
}

// What a refusal says.
const (
	outputAbsent     = "Output not found"
	reportAbsent     = "Report not found"
	suggestionAbsent = "Suggestion not found"
)
