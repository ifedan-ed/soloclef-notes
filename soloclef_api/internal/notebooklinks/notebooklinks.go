// Package notebooklinks serves a notebook's live public link: one address
// per notebook that shows the notebook as it is now, to anyone who has it.
//
// The owner's routes, behind the gate:
//
//	GET    /api/v1/notebooks/{notebook_id}/public-link   the link, or null
//	POST   /api/v1/notebooks/{notebook_id}/public-link   make it, re-date it, or give it a new address
//	DELETE /api/v1/notebooks/{notebook_id}/public-link   stop sharing
//	GET    /api/v1/notebook-links                        every live link of the account
//	DELETE /api/v1/notebook-links/{link_id}              stop one, from the settings page
//
// A visitor's routes, which take no credential (Public):
//
//	GET /api/v1/public-notebooks/{share_token}                                              the page
//	GET /api/v1/public-notebooks/{share_token}/artifacts/{artifact_id}/manifest             how an output opens
//	GET /api/v1/public-notebooks/{share_token}/artifacts/{artifact_id}/files/{file_id}/content
//	GET /api/v1/public-notebooks/{share_token}/artifacts/{artifact_id}/download             the output, to save
//	GET /api/v1/public-notebooks/{share_token}/sources/{document_id}                        how a source opens
//	GET /api/v1/public-notebooks/{share_token}/sources/{document_id}/files/{file_id}/content
//
// # The token is the whole of the authorisation
//
// Every visitor's request resolves the token afresh (resolve): an unknown,
// revoked or deleted link is a 404, an expired one a 410, and so is the
// link of an account that has been deactivated. Everything else a visitor
// names -- a source, an output, a file -- is looked for inside that link's
// notebook and nowhere else, so an id that exists outside it is the same
// 404 as one that does not exist. Nothing here writes for a visitor, starts
// a job or calls a model: the chat a visitor sees is the stored one, read.
//
// # What a visitor is never sent
//
// Account ids, addresses, storage keys and backends, paths, the notebook's
// chat settings and prompts, tool calls and their arguments, reasoning, and
// the id of any cited passage. A citation to one of the notebook's sources
// is renumbered from 1; a citation to anything else is removed (chat.go).
//
// # What it does better than the Python
//
//   - **Sharing is one statement** (ShareNotebook): made, kept or given a
//     new address with no read-then-write, so two presses of Share cannot
//     race into the unique constraint as the Python's could.
//   - **The page is six statements, however long the lists.** The Python
//     listed outputs in three, read the sources twice and looked each
//     link's creator up one by one.
//   - **The source list has a total order**: the order added, then id. The
//     Python read the notebook's sources with no ORDER BY.
//   - **Files honour Range and validators and outlast the write timeout**,
//     through the code the artifact routes stream with (objecthttp,
//     rollingdeadline). The Python download was a generator with no length.
//   - **A deactivated account's links stop**, and an output whose artifact
//     is being deleted is not offered.
//   - **A regenerated or revived link is dated as the new link it is**.
//
// The Python also answered these paths with `Access-Control-Allow-Origin:
// *`, for pages on other sites. This tier answers them with the app's own
// CORS policy like every other route: the public page is the app's own
// page, and one policy is one thing to reason about.
package notebooklinks

import "net/http"

// The owner's paths.
const (
	NotebookLinkPath = "/api/v1/notebooks/{notebook_id}/public-link"
	LinksPath        = "/api/v1/notebook-links"
	LinkPath         = "/api/v1/notebook-links/{link_id}"
)

// A visitor's paths.
const (
	publicBase         = "/api/v1/public-notebooks"
	PagePath           = publicBase + "/{share_token}"
	ManifestPath       = PagePath + "/artifacts/{artifact_id}/manifest"
	ArtifactFilePath   = PagePath + "/artifacts/{artifact_id}/files/{file_id}/content"
	ArtifactSavePath   = PagePath + "/artifacts/{artifact_id}/download"
	SourcePath         = PagePath + "/sources/{document_id}"
	SourceFilePath     = PagePath + "/sources/{document_id}/files/{file_id}/content"
	publicPagePathStem = "/public/notebook/"
)

// Public is the routes that take no credential, as "METHOD /path" pairs:
// the ones to mount outside the gate.
func Public() []string {
	return []string{
		http.MethodGet + " " + PagePath,
		http.MethodGet + " " + ManifestPath,
		http.MethodGet + " " + ArtifactFilePath,
		http.MethodGet + " " + ArtifactSavePath,
		http.MethodGet + " " + SourcePath,
		http.MethodGet + " " + SourceFilePath,
	}
}

// Served is every route, the public ones included.
func Served() []string {
	return append([]string{
		http.MethodGet + " " + NotebookLinkPath,
		http.MethodPost + " " + NotebookLinkPath,
		http.MethodDelete + " " + NotebookLinkPath,
		http.MethodGet + " " + LinksPath,
		http.MethodDelete + " " + LinkPath,
	}, Public()...)
}

// What a caller is told. A visitor is told one thing whatever was wrong
// with what they asked for, so the answer says nothing about what exists.
const (
	notebookAbsent = "Notebook not found"
	linkAbsent     = "Public link not found"
	gone           = "Not found"
	expired        = "This link has expired"
)

const (
	// maxTokenChars is the column's width: a longer token names no link.
	maxTokenChars = 128
	// maxBodyBytes is far more than the share request's two fields need.
	maxBodyBytes = 4 << 10
)
