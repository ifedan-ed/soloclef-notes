package scrapers

// The web crawl tool.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func webDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{{
		Name:        "soloclef_web_crawl",
		Title:       "Crawl web pages",
		Description: WebCrawlDescription,
		Annotations: tooldefinition.ScrapeOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "web_crawlArguments",
			Parameters: parameters(
				toolschema.Parameter{
					Name:        "start_urls",
					Title:       "Start Urls",
					Description: "Full URLs to fetch, e.g. ['https://example.com/blog/post'].",
					Kind:        toolschema.TextList,
					Required:    true,
					MinItems:    toolschema.Limit(1),
				},
				toolschema.Parameter{
					Name:  "max_crawl_depth",
					Title: "Max Crawl Depth",
					Description: "Link-hops to follow from start_urls within the " +
						"same site. 0 fetches only start_urls.",
					Kind:    toolschema.Number,
					Default: 0,
					Minimum: toolschema.Limit(0),
				},
				toolschema.Parameter{
					Name:        "max_crawl_pages",
					Title:       "Max Crawl Pages",
					Description: "Stop after this many pages in total.",
					Kind:        toolschema.Number,
					Default:     10,
					Minimum:     toolschema.Limit(1),
				},
				toolschema.Parameter{
					Name:        "max_length",
					Title:       "Max Length",
					Description: "Max characters kept per page.",
					Kind:        toolschema.Number,
					Default:     50_000,
					Minimum:     toolschema.Limit(1),
				},
				toolschema.Parameter{
					Name:  "include_url_patterns",
					Title: "Include Url Patterns",
					Description: "Regexes; only discovered links matching one are " +
						"followed, e.g. ['/docs/.*'].",
					Kind:     toolschema.TextList,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:        "exclude_url_patterns",
					Title:       "Exclude Url Patterns",
					Description: "Regexes; discovered links matching one are skipped.",
					Kind:        toolschema.TextList,
					Nullable:    true,
				},
			),
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return runCapability(ctx, client, selection, arguments, "web", "crawl",
				pythonjson.NewObject(
					pythonjson.Field("startUrls", textList(arguments, "start_urls")),
					pythonjson.Field("maxCrawlDepth", pythonjson.Num(arguments.Number("max_crawl_depth"))),
					pythonjson.Field("maxCrawlPages", pythonjson.Num(arguments.Number("max_crawl_pages"))),
					pythonjson.Field("maxLength", pythonjson.Num(arguments.Number("max_length"))),
					pythonjson.Field("includeUrlPatterns", textList(arguments, "include_url_patterns")),
					pythonjson.Field("excludeUrlPatterns", textList(arguments, "exclude_url_patterns")),
				))
		},
	}}
}
