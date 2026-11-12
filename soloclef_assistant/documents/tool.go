package documents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// ToolName is what the model calls the search, and what the page's card for
// it is keyed on.
const ToolName = "search_knowledge_base"

const toolDescription = `Search the user's own documents (uploaded files, notes, saved pages and generated artifacts) for passages relevant to a query, by meaning and by keyword.

Use this first to ground any factual answer about the user's content. It returns a <retrieved_context> block in which each matching passage is labelled [n]. Cite a passage by writing its [n] right after the statement it supports.

Write a focused query naming the concrete entities, terms, people or projects you are looking for. Search again with different words if the first results do not answer the question.`

var toolParameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Focused search query with the concrete entities and terms to look for."},"top_k":{"type":"integer","minimum":1,"description":"Maximum number of documents to return. Default 5."}},"required":["query"]}`)

// defaultTopK is how many documents a search returns when the model does
// not say: enough for most questions, small enough to leave room to think.
const defaultTopK = 5

// Tool is the search as the model's tool, for one turn: the account it
// searches and the scope the person's mentions confined it to are fixed
// here, by the server, and are not arguments the model could change.
//
// It asks for no approval (it only reads the caller's own account) and is
// safe beside other calls, so a model that asks three questions at once has
// them searched at once.
func (s *Searcher) Tool(userID string, scope Scope) tool.Tool {
	return tool.Tool{
		Name:        ToolName,
		Description: toolDescription,
		Parameters:  toolParameters,
		Approval:    tool.Auto,
		Parallel:    true,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Query string `json:"query"`
				TopK  *int   `json:"top_k"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return "", fmt.Errorf("the arguments are not readable: %w", err)
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return "", fmt.Errorf("provide a non-empty search query")
			}
			topK := defaultTopK
			if args.TopK != nil {
				topK = *args.TopK
			}
			hits, outcome, err := s.SearchScoped(ctx, userID, query, scope, topK)
			if err != nil {
				return "", err
			}
			// Three ways of having nothing to show are three different
			// things to tell the person, so the model is told which.
			switch {
			case outcome == NoSources:
				return "This conversation has no sources to search: its notebook has no enabled sources. Tell the user nothing can be found until they add sources to the notebook or tick the ones already in it.", nil
			case outcome == OutsideLimit:
				return "The documents the user mentioned are not among this notebook's enabled sources, so they were not searched. Tell the user so, and that a document is searched here once it is added to the notebook as a source.", nil
			case len(hits) == 0:
				where := "the user's documents"
				switch {
				case scope.mentions():
					where = "the documents the user mentioned"
				case scope.Limited:
					where = "this notebook's enabled sources"
				}
				return fmt.Sprintf("No matches found in %s for query: %q.\nTry a different query, or tell the user nothing relevant was found.", where, query), nil
			}
			return Render(hits, call.Citations), nil
		},
	}
}
