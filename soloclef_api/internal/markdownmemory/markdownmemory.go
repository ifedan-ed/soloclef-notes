// Package markdownmemory serves the markdown document the chat keeps about a
// signed-in account: one memory per account, in "user".memory_md.
//
//	GET  /api/v1/users/me/memory
//	PUT  /api/v1/users/me/memory
//	POST /api/v1/users/me/memory/reset
//
// The routes owe nothing outside that column, and no model is reached: the
// save pipeline here is the validation and the re-render only.
//
// What makes the port deep rather than wide is that a write *rewrites* what it
// is given. save_memory strips a preamble, refuses a document that is too long
// or has no heading, and then re-renders the document through app/services/memory/document.py's parser —
// which normalises a legacy bullet and an ISO week date into a plain date. So
// the bytes a client gets back are the parser's, and the parser rests on three
// pieces of Python behaviour that Go does not share: str.strip's whitespace
// set, str.splitlines' line boundaries, and date.fromisoformat's acceptance
// of a week date. Each is mirrored in its own
// file beside this one, with the vectors that came out of the running tier.
package markdownmemory

// The limits app/services/memory/validation.py declares, which the response
// carries on every route.
const (
	SoftLimit = 18_000
	HardLimit = 25_000
)
