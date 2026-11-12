// Package instructionslint warns when an account's chat instructions try to
// do more than set style.
//
//	POST /api/v1/system-instructions/lint  {"text": "...", "use_model": true}
//	  -> {"warnings": [{"code", "quote", "source"}], "model_checked": bool}
//
// A person may shape tone, audience, format and length. Text that tries to
// turn citations off, answer from outside the sources, change safety
// behaviour or play prompt tricks is flagged, so the settings page can say
// so beside the text. It is only ever a warning: saving stays allowed, and
// what keeps such text from working is the prompt's own precedence, not
// this check.
//
// # Two passes
//
// Fixed patterns, always (rules.go). And, when asked, a short reading by
// the account's quick model (internal/modelchoices), which catches
// phrasings the patterns miss and adds only the kinds of warning the
// patterns did not already give.
//
// The model pass is advice on top of a complete answer, so when it cannot
// be had (no quick model on the proxy, a slow or failed call, a reply that
// is not the JSON asked for) the pattern warnings are answered alone and
// `model_checked` is false. That is said in the answer rather than hidden:
// the page shows "checked by rules only". The reason goes to the log.
//
// # What changed from the Python route
//
// The text limit is the limit instructions are saved with
// (accountprofile.InstructionsMaxLength), counted after the same clean-up,
// so the check never refuses a text the save would accept. The model's
// reply is read strictly: a code outside the four is dropped, not passed to
// a page that has no sentence for it.
package instructionslint

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/accountprofile"

// Path is the route.
const Path = "/api/v1/system-instructions/lint"

// MaxChars is the most text checked, in characters: the length chat
// instructions are saved with, by name, so the two cannot drift apart.
const MaxChars = accountprofile.InstructionsMaxLength

// Code is the kind of thing a warning is about. The web app has one
// sentence per code (features/chat-instructions/lib/instructions.ts).
type Code string

// The codes, in the order warnings are given.
const (
	Citations        Code = "citations"
	OutsideKnowledge Code = "outside_knowledge"
	Safety           Code = "safety"
	PromptInjection  Code = "prompt_injection"
)

var codes = []Code{Citations, OutsideKnowledge, Safety, PromptInjection}

// Warning is one flagged passage.
type Warning struct {
	Code Code `json:"code"`
	// Quote is the passage with a little of what surrounds it, on one line.
	Quote string `json:"quote"`
	// Source is "rule" for a pattern and "model" for the model's reading.
	Source string `json:"source"`
}

// quoteChars bounds a quote; quoteContext is how much text either side of
// a pattern's match is shown with it.
const (
	quoteChars   = 120
	quoteContext = 30
)
