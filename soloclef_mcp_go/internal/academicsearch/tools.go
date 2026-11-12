// Package academicsearch exposes PubMed, arXiv and medRxiv/bioRxiv search,
// standalone.
//
// Thin wrappers over the API's three /academic-search/<source> routes, which
// run the same sources the chat's own tools search, as the caller's account,
// without creating a notebook or an import.
//
// There is no Google Scholar tool. Scholar has no API; the tool this server
// once published worked by scraping its result pages, which Google's terms
// forbid and its wall soon blocks, and the API no longer does that. A tool
// that could only ever answer "unavailable" would cost a model a turn each
// time it was tried, so it was taken off the surface, and out of the
// instructions and the documentation, in the same change.
package academicsearch

import (
	"context"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/rendering"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

// queryParameter and resultsParameter are shared by all three tools, as the
// Python annotations are.
func queryParameter() toolschema.Parameter {
	return toolschema.Parameter{
		Name:        "query",
		Title:       "Query",
		Description: "Search query.",
		Kind:        toolschema.Text,
		Required:    true,
		MinLength:   toolschema.Limit(1),
	}
}

func resultsParameter() toolschema.Parameter {
	return toolschema.Parameter{
		Name:        "max_results",
		Title:       "Max Results",
		Description: "Maximum papers to return.",
		Kind:        toolschema.Number,
		Default:     10,
		Minimum:     toolschema.Limit(1),
		Maximum:     toolschema.Limit(30),
	}
}

// Definitions are the three academic search tools, in the order they are
// published.
func Definitions(client *restclient.Client) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		pubmed(client),
		arxiv(client),
		preprint(client),
	}
}

func pubmed(client *restclient.Client) tooldefinition.Definition {
	return tooldefinition.Definition{
		Name:        "soloclef_pubmed_search",
		Title:       "Search PubMed",
		Description: PubmedSearchDescription,
		Annotations: tooldefinition.ReadOnlyOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "pubmed_searchArguments",
			Parameters: []toolschema.Parameter{
				queryParameter(),
				resultsParameter(),
				{
					Name:        "since",
					Title:       "Since",
					Description: "Earliest publication year, inclusive.",
					Kind:        toolschema.Number,
					Nullable:    true,
				},
				{
					Name:        "until",
					Title:       "Until",
					Description: "Latest publication year, inclusive.",
					Kind:        toolschema.Number,
					Nullable:    true,
				},
				{
					Name:  "full_text",
					Title: "Full Text",
					Description: "When true, read PMC open-access full text for the " +
						"top hits (where available) and return an excerpt instead of " +
						"just the abstract.",
					Kind:    toolschema.Flag,
					Default: false,
				},
				rendering.FormatParameter(),
			},
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			since, sinceGiven := arguments.OptionalNumber("since")
			until, untilGiven := arguments.OptionalNumber("until")
			return search(ctx, client, arguments, "pubmed", pythonjson.NewObject(
				pythonjson.Field("query", pythonjson.Str(arguments.Text("query"))),
				pythonjson.Field("max_results", pythonjson.Num(arguments.Number("max_results"))),
				pythonjson.Field("since", pythonjson.OptionalNum(since, sinceGiven)),
				pythonjson.Field("until", pythonjson.OptionalNum(until, untilGiven)),
				pythonjson.Field("full_text", pythonjson.Flag(arguments.Flag("full_text"))),
			))
		},
	}
}

func arxiv(client *restclient.Client) tooldefinition.Definition {
	return tooldefinition.Definition{
		Name:        "soloclef_arxiv_search",
		Title:       "Search arXiv",
		Description: ArxivSearchDescription,
		Annotations: tooldefinition.ReadOnlyOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "arxiv_searchArguments",
			Parameters: []toolschema.Parameter{
				queryParameter(),
				resultsParameter(),
				rendering.FormatParameter(),
			},
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return search(ctx, client, arguments, "arxiv", queryAndCount(arguments))
		},
	}
}

func preprint(client *restclient.Client) tooldefinition.Definition {
	return tooldefinition.Definition{
		Name:        "soloclef_preprint_search",
		Title:       "Search medRxiv / bioRxiv",
		Description: PreprintSearchDescription,
		Annotations: tooldefinition.ReadOnlyOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "preprint_searchArguments",
			Parameters: []toolschema.Parameter{
				queryParameter(),
				{
					Name:        "server",
					Title:       "Server",
					Description: "Which preprint server to search.",
					Kind:        toolschema.Text,
					Choices:     []string{"medrxiv", "biorxiv"},
					Required:    true,
				},
				resultsParameter(),
				rendering.FormatParameter(),
			},
		},
		// The source in the path is always medrxiv: the backend's medRxiv source
		// serves both servers and picks between them from the body's "server".
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return search(ctx, client, arguments, "medrxiv", pythonjson.NewObject(
				pythonjson.Field("query", pythonjson.Str(arguments.Text("query"))),
				pythonjson.Field("max_results", pythonjson.Num(arguments.Number("max_results"))),
				pythonjson.Field("server", pythonjson.Str(arguments.Text("server"))),
			))
		},
	}
}

func queryAndCount(arguments toolschema.Arguments) pythonjson.Value {
	return pythonjson.NewObject(
		pythonjson.Field("query", pythonjson.Str(arguments.Text("query"))),
		pythonjson.Field("max_results", pythonjson.Num(arguments.Number("max_results"))),
	)
}

// search runs one source and renders its rows. The body is sent as written,
// nulls included, because the backend's academic door reads an absent year and
// a null year the same way and the Python original does not strip them.
func search(ctx context.Context, client *restclient.Client, arguments toolschema.Arguments,
	source string, body pythonjson.Value) (string, error) {
	reply, err := client.Do(ctx, restclient.Call{
		Method:   "POST",
		Path:     "/academic-search/" + source,
		JSONBody: body,
	})
	if err != nil {
		return "", err
	}
	rows := []pythonjson.Value{}
	if reply.Kind == pythonjson.Array {
		rows = reply.Array
	}
	if rendering.Chosen(arguments) == rendering.JSON {
		return rendering.ToJSON(pythonjson.Value{Kind: pythonjson.Array, Array: rows}), nil
	}
	return render(arguments.Text("query"), rows), nil
}

func render(query string, rows []pythonjson.Value) string {
	if len(rows) == 0 {
		return fmt.Sprintf("No results for \"%s\".", query)
	}
	text := &rendering.Text{}
	text.Line("# %d result(s) for \"%s\"", len(rows), query)
	text.Raw("")
	for _, row := range rows {
		paper, _ := row.Lookup("paper")
		text.Line("## %s", pythonjson.Text(row, "title", "Untitled"))
		if meta := pythonjson.TruthyText(row, "meta"); meta != "" {
			text.Raw(meta)
		}
		if ids := identifiers(paper); ids != "" {
			text.Raw(ids)
		}
		if url := pythonjson.TruthyText(row, "url"); url != "" {
			text.Raw(url)
		}
		if snippet := strings.TrimSpace(pythonjson.TruthyText(row, "snippet")); snippet != "" {
			text.Line("> %s", snippet)
		}
		text.Raw("")
	}
	return text.Trimmed()
}

// identifierLabels are printed in this order, which is the order the Python
// renderer walks them in.
var identifierLabels = []struct{ key, label string }{
	{"doi", "DOI"},
	{"pmid", "PMID"},
	{"pmcid", "PMCID"},
	{"arxiv_id", "arXiv"},
}

func identifiers(paper pythonjson.Value) string {
	var parts []string
	for _, pair := range identifierLabels {
		if value := pythonjson.TruthyText(paper, pair.key); value != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", pair.label, value))
		}
	}
	return strings.Join(parts, " · ")
}
