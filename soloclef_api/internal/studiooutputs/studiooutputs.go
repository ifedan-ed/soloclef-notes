// Package studiooutputs serves a notebook's studio outputs as the Studio
// panel lists and manages them:
//
//	GET    /api/v1/notebooks/{notebook_id}/studio/outputs               the outputs, newest first
//	PATCH  /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}   rename one
//	DELETE /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}   stop it and remove what it made
//
// Starting an output (`…/studio/generate`) is internal/studiogenerate's,
// which answers with this package's rendering of the new row (One).
//
// # What it does better than the Python
//
//   - **One statement lists the outputs with their runs and artifacts**,
//     where the Python read them in two and then a third for the formats.
//   - **A delete is one transaction**: the output goes, its run is stopped
//     (a queued job cancelled outright, a running one asked to stop, an
//     unfinished podcast cancelled) and the artifact's document is claimed
//     for removal, together. The removal of the artifact's stored files and
//     vectors then runs before the route answers, as the artifact delete
//     does, instead of being a second request that could fail unseen.
//   - **Every statement names the owner**, joins included.
package studiooutputs

import "net/http"

// The paths.
const (
	OutputsPath = "/api/v1/notebooks/{notebook_id}/studio/outputs"
	OutputPath  = "/api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + OutputsPath,
		http.MethodPatch + " " + OutputPath,
		http.MethodDelete + " " + OutputPath,
	}
}

const (
	notebookAbsent = "Notebook not found"
	outputAbsent   = "Output not found"
	maxTitleChars  = 200
)
