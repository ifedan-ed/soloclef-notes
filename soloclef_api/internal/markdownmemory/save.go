package markdownmemory

import "strings"

// noUpdateSentinels is service.py's _NO_UPDATE_SENTINELS: four words a model
// writes to mean "leave it alone". A request carrying one changes nothing and
// is answered with the stored document, which is a 200 rather than a refusal.
var noUpdateSentinels = map[string]bool{
	"NO_UPDATE": true,
	"NO UPDATE": true,
	"NO_CHANGE": true,
	"NO CHANGE": true,
}

// Outcome is what save_memory decided: either the document to store and answer
// with, or the message the route turns into a 400, or neither — the no-op,
// which answers with what is already stored.
type Outcome struct {
	// Store is the text to write, meaningful only when Write is true.
	Store string
	// Write is false for a no-op and for a refusal.
	Write bool
	// Refusal is the Python route's own message, empty when there is none.
	Refusal string
	// Answer is the document the response carries.
	Answer string
}

// Save is app/services/memory/service.py:save_memory with llm=None, which is
// how all six routes call it — so the forced LLM rewrite is unreachable and no
// model is involved.
//
// The order is the Python function's and is load-bearing: the preamble is
// stripped before the sentinel is looked for, so "  no update  " is a no-op;
// the size is checked before the heading rule, so a 30,000-character document
// with no heading is refused for its length.
func Save(requested, stored string) Outcome {
	next := strippedPreamble(pythonStrip(requested))

	// strings.ToUpper is Go's simple case mapping and Python's str.upper() is
	// the full one, which expands a few characters ("ß" to "SS", "ﬁ" to "FI").
	// No expansion can produce one of the four words above, so the difference
	// is unreachable here; it is noted because the same call in a comparison
	// against a wider vocabulary would not be safe.
	if noUpdateSentinels[strings.ToUpper(next)] {
		return Outcome{Answer: stored}
	}
	if message, refused := tooLong(next); refused {
		return Outcome{Refusal: message, Answer: stored}
	}
	if message, refused := headingless(next); refused {
		return Outcome{Refusal: message, Answer: stored}
	}

	// The document is re-rendered before it is written, which is what
	// normalises a legacy bullet and an ISO week date and what drops a
	// headingless line.
	rendered := renderDocument(parseDocument(next))
	return Outcome{Store: rendered, Write: true, Answer: rendered}
}
