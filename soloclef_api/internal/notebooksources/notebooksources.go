// Package notebooksources serves the documents and folders a notebook reads
// from, and whether each is ticked:
//
//	GET    /api/v1/notebooks/{notebook_id}/sources               the sources, in the order added
//	POST   /api/v1/notebooks/{notebook_id}/sources               add documents and folders
//	PATCH  /api/v1/notebooks/{notebook_id}/sources/{source_id}   tick or untick one
//	POST   /api/v1/notebooks/{notebook_id}/sources/toggle        tick or untick all, or a listed few
//	DELETE /api/v1/notebooks/{notebook_id}/sources/{source_id}   remove one (the document stays)
//
// # Whose sources
//
// A notebook, its sources and the documents and folders they point at are
// one account's. Every statement names the owner, and the schema's composite
// keys refuse a source that would join two accounts, so another account's
// notebook is the same 404 as one never made, and another account's
// document cannot be added: it is "not found" like an id nobody has.
//
// # What it does better than the Python
//
//   - **An add is all or nothing**, in one transaction: either every target
//     is the caller's and every link is written, or none is.
//   - **Untick-all with a list touches only that notebook's sources**; ids of
//     another notebook's sources in the list change nothing.
//   - **Every write moves the notebook up its owner's list**, the toggles
//     included (the Python moved it only on add and remove).
package notebooksources

import "net/http"

// The paths.
const (
	SourcesPath = "/api/v1/notebooks/{notebook_id}/sources"
	SourcePath  = "/api/v1/notebooks/{notebook_id}/sources/{source_id}"
	TogglePath  = "/api/v1/notebooks/{notebook_id}/sources/toggle"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + SourcesPath,
		http.MethodPost + " " + SourcesPath,
		http.MethodPatch + " " + SourcePath,
		http.MethodPost + " " + TogglePath,
		http.MethodDelete + " " + SourcePath,
	}
}

// The refusals.
const (
	notebookAbsent = "Notebook not found"
	sourceAbsent   = "Source not found"
)

// maxTargets bounds one add: a picker that sends more is a bug or an attack.
const maxTargets = 500
