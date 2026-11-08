package scrapers

// The TikTok tools: videos, comments, account search, and trending.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func maxItems(description string, fallback int) toolschema.Parameter {
	return toolschema.Parameter{
		Name:        "max_items",
		Title:       "Max Items",
		Description: description,
		Kind:        toolschema.Number,
		Default:     fallback,
		Minimum:     toolschema.Limit(1),
	}
}

func tiktokDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_tiktok_scrape",
			Title:       "Scrape TikTok videos",
			Description: TiktokScrapeDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "tiktok_scrapeArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "urls",
						Title: "Urls",
						Description: "TikTok URLs: a video, a profile " +
							"('https://www.tiktok.com/@nasa'), a hashtag " +
							"('https://www.tiktok.com/tag/food'), or a search URL. Provide " +
							"urls OR profiles/hashtags.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:  "profiles",
						Title: "Profiles",
						Description: "Profile usernames to scrape, with or without a " +
							"leading '@', e.g. ['nasa'].",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:        "hashtags",
						Title:       "Hashtags",
						Description: "Hashtag names to scrape, without the '#', e.g. ['food'].",
						Kind:        toolschema.TextList,
						Nullable:    true,
					},
					toolschema.Parameter{
						Name:        "results_per_page",
						Title:       "Results Per Page",
						Description: "Max videos per profile/hashtag target.",
						Kind:        toolschema.Number,
						Default:     10,
						Minimum:     toolschema.Limit(1),
					},
					maxItems("Maximum videos to return in total.", 10),
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "tiktok", "scrape",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("profiles", textList(arguments, "profiles")),
						pythonjson.Field("hashtags", textList(arguments, "hashtags")),
						pythonjson.Field("results_per_page", pythonjson.Num(arguments.Number("results_per_page"))),
						pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					))
			},
		},
		{
			Name:        "soloclef_tiktok_comments",
			Title:       "Scrape TikTok comments",
			Description: TiktokCommentsDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "tiktok_commentsArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "video_urls",
						Title: "Video Urls",
						Description: "TikTok video URLs " +
							"('https://www.tiktok.com/@user/video/123') to pull comments from.",
						Kind:     toolschema.TextList,
						Required: true,
					},
					toolschema.Parameter{
						Name:        "comments_per_video",
						Title:       "Comments Per Video",
						Description: "Max comments to return per video.",
						Kind:        toolschema.Number,
						Default:     20,
						Minimum:     toolschema.Limit(1),
					},
					maxItems("Maximum comments to return in total.", 20),
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "tiktok", "comments",
					pythonjson.NewObject(
						pythonjson.Field("video_urls", textList(arguments, "video_urls")),
						pythonjson.Field("comments_per_video", pythonjson.Num(arguments.Number("comments_per_video"))),
						pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					))
			},
		},
		{
			Name:        "soloclef_tiktok_user_search",
			Title:       "Search TikTok accounts",
			Description: TiktokUserSearchDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "tiktok_user_searchArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "queries",
						Title: "Queries",
						Description: "Keywords to find TikTok accounts by, e.g. " +
							"['nasa', 'cooking'].",
						Kind:     toolschema.TextList,
						Required: true,
					},
					toolschema.Parameter{
						Name:        "results_per_query",
						Title:       "Results Per Query",
						Description: "Max accounts to return per query.",
						Kind:        toolschema.Number,
						Default:     10,
						Minimum:     toolschema.Limit(1),
					},
					maxItems("Maximum accounts to return in total.", 10),
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "tiktok", "user_search",
					pythonjson.NewObject(
						pythonjson.Field("queries", textList(arguments, "queries")),
						pythonjson.Field("results_per_query", pythonjson.Num(arguments.Number("results_per_query"))),
						pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					))
			},
		},
		{
			Name:        "soloclef_tiktok_trending",
			Title:       "Get trending TikTok videos",
			Description: TiktokTrendingDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "tiktok_trendingArguments",
				Parameters: parameters(
					maxItems("Max trending videos to return from Explore.", 20),
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "tiktok", "trending",
					pythonjson.NewObject(
						pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					))
			},
		},
	}
}
