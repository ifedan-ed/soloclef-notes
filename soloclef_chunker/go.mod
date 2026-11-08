// The text chunker that decides a stored chunk's boundaries, in Go.
//
// It is a module of its own, required with a relative replace by whichever
// tier ends up owning indexing, for the same reason soloclef_celery is one:
// the boundaries it cuts are already in the database, so there has to be one
// spelling of them, and `internal` cannot be shared across modules
// (soloclef_chunker/AGENTS.md, "Why it is a module and not a package").
//
// It has no dependencies, on purpose: a chunk boundary is decided from the
// text alone, so nothing here reads configuration, a database or a network.
module git.danvics.com/danvics/soloclef/soloclef_chunker

go 1.24.4
