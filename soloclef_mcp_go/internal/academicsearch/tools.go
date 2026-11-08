// Package academicsearch exposes PubMed, arXiv, medRxiv/bioRxiv and Google
// Scholar search, standalone.
//
// Thin wrappers over the backend's /workspaces/{id}/academic-search/{source}
// door, which reuses the notebook research academic sources without creating a
// notebook or an import. Not billed, which matches the backend today.
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
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

// queryParameter and resultsParameter are shared by all four tools, as the
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

// Definitions are the four academic search tools, in the order they are
// published.
func Definitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		pubmed(client, selection),
		arxiv(client, selection),
		preprint(client, selection),
		scholar(client, selection),
	}
}

func pubmed(client *restclient.Client,
	selection *workspaceselection.Selection) tooldefinition.Definition {
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
				workspaceselection.ReferenceParameter(),
				rendering.FormatParameter(),
			},
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			since, sinceGiven := arguments.OptionalNumber("since")
			until, untilGiven := arguments.OptionalNumber("until")
			return search(ctx, client, selection, arguments, "pubmed", pythonjson.NewObject(
				pythonjson.Field("query", pythonjson.Str(arguments.Text("query"))),
				pythonjson.Field("max_results", pythonjson.Num(arguments.Number("max_results"))),
				pythonjson.Field("since", pythonjson.OptionalNum(since, sinceGiven)),
				pythonjson.Field("until", pythonjson.OptionalNum(until, untilGiven)),
				pythonjson.Field("full_text", pythonjson.Flag(arguments.Flag("full_text"))),
			))
		},
	}
}

func arxiv(client *restclient.Client,
	selection *workspaceselection.Selection) tooldefinition.Definition {
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
				workspaceselection.ReferenceParameter(),
				rendering.FormatParameter(),
			},
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return search(ctx, client, selection, arguments, "arxiv", queryAndCount(arguments))
		},
	}
}

func preprint(client *restclient.Client,
	selection *workspaceselection.Selection) tooldefinition.Definition {
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
				workspaceselection.ReferenceParameter(),
				rendering.FormatParameter(),
			},
		},
		// The source in the path is always medrxiv: the backend's medRxiv source
		// serves both servers and picks between them from the body's "server".
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return search(ctx, client, selection, arguments, "medrxiv", pythonjson.NewObject(
				pythonjson.Field("query", pythonjson.Str(arguments.Text("query"))),
				pythonjson.Field("max_results", pythonjson.Num(arguments.Number("max_results"))),
				pythonjson.Field("server", pythonjson.Str(arguments.Text("server"))),
			))
		},
	}
}

func scholar(client *restclient.Client,
	selection *workspaceselection.Selection) tooldefinition.Definition {
	return tooldefinition.Definition{
		Name:        "soloclef_scholar_search",
		Title:       "Search Google Scholar",
		Description: ScholarSearchDescription,
		Annotations: tooldefinition.ReadOnlyOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "scholar_searchArguments",
			Parameters: []toolschema.Parameter{
				queryParameter(),
				resultsParameter(),
				workspaceselection.ReferenceParameter(),
				rendering.FormatParameter(),
			},
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return search(ctx, client, selection, arguments, "google_scholar", queryAndCount(arguments))
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
func search(ctx context.Context, client *restclient.Client,
	selection *workspaceselection.Selection, arguments toolschema.Arguments,
	source string, body pythonjson.Value) (string, error) {
	workspace, err := selection.Resolve(ctx, arguments.Text("workspace"))
	if err != nil {
		return "", err
	}
	reply, err := client.Do(ctx, restclient.Call{
		Method:   "POST",
		Path:     fmt.Sprintf("/workspaces/%d/academic-search/%s", workspace.ID, source),
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
