package scrapers

// The YouTube tools: videos and comments.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func youtubeDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_youtube_scrape",
			Title:       "Search or scrape YouTube",
			Description: YoutubeScrapeDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "youtube_scrapeArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "urls",
						Title: "Urls",
						Description: "YouTube URLs: video, channel, playlist, shorts, " +
							"or hashtag pages. Provide urls OR search_queries.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:  "search_queries",
						Title: "Search Queries",
						Description: "Terms to search YouTube for, e.g. " +
							"['NotebookLM tutorial']. Provide search_queries OR urls.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:        "max_results",
						Title:       "Max Results",
						Description: "Maximum videos to return.",
						Kind:        toolschema.Number,
						Default:     10,
						Minimum:     toolschema.Limit(1),
					},
					toolschema.Parameter{
						Name:        "download_subtitles",
						Title:       "Download Subtitles",
						Description: "True also fetches each video's transcript.",
						Kind:        toolschema.Flag,
						Default:     false,
					},
					toolschema.Parameter{
						Name:        "subtitles_language",
						Title:       "Subtitles Language",
						Description: "Transcript language code, e.g. 'en'.",
						Kind:        toolschema.Text,
						Default:     "en",
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "youtube", "scrape",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("search_queries", textList(arguments, "search_queries")),
						pythonjson.Field("max_results", pythonjson.Num(arguments.Number("max_results"))),
						pythonjson.Field("download_subtitles", pythonjson.Flag(arguments.Flag("download_subtitles"))),
						pythonjson.Field("subtitles_language", pythonjson.Str(arguments.Text("subtitles_language"))),
					))
			},
		},
		{
			Name:        "soloclef_youtube_comments",
			Title:       "Fetch YouTube comments",
			Description: YoutubeCommentsDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "youtube_commentsArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "urls",
						Title: "Urls",
						Description: "YouTube video URLs, e.g. " +
							"['https://www.youtube.com/watch?v=abc123'].",
						Kind:     toolschema.TextList,
						Required: true,
						MinItems: toolschema.Limit(1),
					},
					toolschema.Parameter{
						Name:  "max_comments",
						Title: "Max Comments",
						Description: "Maximum comments per video, counting top-level " +
							"comments and replies together.",
						Kind:    toolschema.Number,
						Default: 20,
						Minimum: toolschema.Limit(1),
					},
					toolschema.Parameter{
						Name:        "sort_by",
						Title:       "Sort By",
						Description: "Comment ordering.",
						Kind:        toolschema.Text,
						Choices:     []string{"TOP_COMMENTS", "NEWEST_FIRST"},
						Default:     "NEWEST_FIRST",
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "youtube", "comments",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("max_comments", pythonjson.Num(arguments.Number("max_comments"))),
						pythonjson.Field("sort_by", pythonjson.Str(arguments.Text("sort_by"))),
					))
			},
		},
	}
}
