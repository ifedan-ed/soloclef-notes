package scrapers

// The Google Search tool.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func googleSearchDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{{
		Name:        "soloclef_google_search",
		Title:       "Scrape Google Search",
		Description: GoogleSearchDescription,
		Annotations: tooldefinition.ScrapeOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "google_searchArguments",
			Parameters: parameters(
				toolschema.Parameter{
					Name:  "queries",
					Title: "Queries",
					Description: "Search terms or full Google Search URLs, e.g. " +
						"['best rss readers 2026'].",
					Kind:     toolschema.TextList,
					Required: true,
					MinItems: toolschema.Limit(1),
				},
				toolschema.Parameter{
					Name:        "max_pages_per_query",
					Title:       "Max Pages Per Query",
					Description: "Result pages to fetch per query.",
					Kind:        toolschema.Number,
					Default:     1,
					Minimum:     toolschema.Limit(1),
				},
				toolschema.Parameter{
					Name:        "country_code",
					Title:       "Country Code",
					Description: "Two-letter country to search from, e.g. 'us'.",
					Kind:        toolschema.Text,
					Nullable:    true,
				},
				toolschema.Parameter{
					Name:        "language_code",
					Title:       "Language Code",
					Description: "Results language, e.g. 'en'. Empty for default.",
					Kind:        toolschema.Text,
					Default:     "",
				},
				toolschema.Parameter{
					Name:        "site",
					Title:       "Site",
					Description: "Restrict results to one domain, e.g. 'example.com'.",
					Kind:        toolschema.Text,
					Nullable:    true,
				},
			),
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return runCapability(ctx, client, selection, arguments, "google_search", "scrape",
				pythonjson.NewObject(
					pythonjson.Field("queries", textList(arguments, "queries")),
					pythonjson.Field("max_pages_per_query", pythonjson.Num(arguments.Number("max_pages_per_query"))),
					pythonjson.Field("country_code", optionalText(arguments, "country_code")),
					pythonjson.Field("language_code", pythonjson.Str(arguments.Text("language_code"))),
					pythonjson.Field("site", optionalText(arguments, "site")),
				))
		},
	}}
}
