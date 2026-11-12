package notebookresearch

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/indexqueue"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// The pieces cmd/api hands these routes are the chat's own and the
// indexing queue's. If one of them changes shape, this fails to compile
// here, beside the interfaces, and not only in cmd/api.
var (
	_ Papers     = (*academic.Sources)(nil)
	_ WebSearch  = websearch.Search{}
	_ PageReader = (*pagereader.Reader)(nil)
	_ Indexing   = indexqueue.Indexing{}
)
