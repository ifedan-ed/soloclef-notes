package scrapers

// The Reddit tool.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func redditDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{{
		Name:        "soloclef_reddit_scrape",
		Title:       "Search or scrape Reddit",
		Description: RedditScrapeDescription,
		Annotations: tooldefinition.ScrapeOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "reddit_scrapeArguments",
			Parameters: parameters(
				toolschema.Parameter{
					Name:  "urls",
					Title: "Urls",
					Description: "Reddit URLs: a post, a subreddit like " +
						"'https://reddit.com/r/LocalLLaMA', a user page, or a search " +
						"URL. Provide urls OR search_queries.",
					Kind:     toolschema.TextList,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:  "search_queries",
					Title: "Search Queries",
					Description: "Terms to search Reddit for, e.g. " +
						"['NotebookLM alternatives']. Provide search_queries OR urls.",
					Kind:     toolschema.TextList,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:  "community",
					Title: "Community",
					Description: "Restrict a search to one subreddit, name without " +
						"'r/', e.g. 'ArtificialInteligence'.",
					Kind:     toolschema.Text,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:        "sort",
					Title:       "Sort",
					Description: "Post ordering.",
					Kind:        toolschema.Text,
					Choices:     []string{"relevance", "hot", "top", "new", "rising", "comments"},
					Default:     "new",
				},
				toolschema.Parameter{
					Name:        "time_filter",
					Title:       "Time Filter",
					Description: "Time window; only valid with sort='top'.",
					Kind:        toolschema.Text,
					Choices:     []string{"hour", "day", "week", "month", "year", "all"},
					Nullable:    true,
				},
				toolschema.Parameter{
					Name:        "max_items",
					Title:       "Max Items",
					Description: "Maximum posts to return.",
					Kind:        toolschema.Number,
					Default:     10,
					Minimum:     toolschema.Limit(1),
				},
				toolschema.Parameter{
					Name:  "skip_comments",
					Title: "Skip Comments",
					Description: "True fetches posts only (faster); False also " +
						"fetches each post's comment thread.",
					Kind:    toolschema.Flag,
					Default: false,
				},
			),
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return runCapability(ctx, client, selection, arguments, "reddit", "scrape",
				pythonjson.NewObject(
					pythonjson.Field("urls", textList(arguments, "urls")),
					pythonjson.Field("search_queries", textList(arguments, "search_queries")),
					pythonjson.Field("community", optionalText(arguments, "community")),
					pythonjson.Field("sort", pythonjson.Str(arguments.Text("sort"))),
					pythonjson.Field("time_filter", optionalText(arguments, "time_filter")),
					pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					pythonjson.Field("skip_comments", pythonjson.Flag(arguments.Flag("skip_comments"))),
				))
		},
	}}
}
