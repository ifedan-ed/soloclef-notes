package chat

import (
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// Parts are the pieces of the chat that other routes run on: the notebook's
// studio, its follow-up questions and source guides, the passage search, the
// research box and the academic search routes.
//
// They are handed out from here, already built and checked, so that there is
// one model menu, one document search, one set of academic sources and one
// artifact specialist in the process. A second copy built from the same
// settings would be a second place for a limit or a prompt to drift, and a
// studio output made by a different specialist than the chat's would be a
// different artifact for the same request.
type Parts struct {
	// Models answers which model an account's requests run on.
	Models *Models
	// Documents is the hybrid search over an account's own documents.
	Documents *documents.Searcher
	// Academic is PubMed, arXiv and the preprint servers, each on its own.
	Academic *academic.Sources
	// Pages reads public web pages, refusing private addresses.
	Pages *pagereader.Reader
	// Web is the account's own web search providers.
	Web websearch.Search
	// Deliverables is the artifact specialist. Its Model is unset: whoever
	// runs it sets the model of the account it runs for.
	Deliverables deliverables.Specialist
	// Checkpoints is the store the specialist's runs are saved to.
	Checkpoints checkpoint.Store
}
