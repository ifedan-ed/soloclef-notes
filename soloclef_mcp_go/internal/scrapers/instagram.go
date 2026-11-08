package scrapers

// The Instagram tools: posts/reels and profile details, anonymous-only.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

// instagramSearchType is the discovery kind both tools take; profile-only,
// because anything else is login-walled.
func instagramSearchType() toolschema.Parameter {
	return toolschema.Parameter{
		Name:        "search_type",
		Title:       "Search Type",
		Description: "Discovery kind (profile-only).",
		Kind:        toolschema.Text,
		Choices:     []string{"profile", "user"},
		Default:     "profile",
	}
}

func instagramDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_instagram_scrape",
			Title:       "Scrape Instagram posts or reels",
			Description: InstagramScrapeDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "instagram_scrapeArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "urls",
						Title: "Urls",
						Description: "Instagram URLs: a profile, post (/p/), or reel " +
							"(/reel/). Hashtag/place URLs are unsupported (login-walled). " +
							"Provide urls OR search_queries.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:  "search_queries",
						Title: "Search Queries",
						Description: "Search words. One word that names an account " +
							"returns that account's latest posts; anything else is searched " +
							"via Google for posts, reels and profiles. Google-backed " +
							"discovery is slow (~30-60s per query), so " +
							"start with at most 3 queries and expand only if nothing " +
							"significant is found. Provide search_queries OR urls.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					instagramSearchType(),
					toolschema.Parameter{
						Name:        "result_type",
						Title:       "Result Type",
						Description: "Which feed to return: 'posts' or 'reels'.",
						Kind:        toolschema.Text,
						Choices:     []string{"posts", "reels"},
						Default:     "posts",
					},
					toolschema.Parameter{
						Name:        "max_items",
						Title:       "Max Items",
						Description: "Maximum items to return across sources.",
						Kind:        toolschema.Number,
						Default:     10,
						Minimum:     toolschema.Limit(1),
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "instagram", "scrape",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("search_queries", textList(arguments, "search_queries")),
						pythonjson.Field("search_type", pythonjson.Str(arguments.Text("search_type"))),
						pythonjson.Field("result_type", pythonjson.Str(arguments.Text("result_type"))),
						pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					))
			},
		},
		{
			Name:        "soloclef_instagram_details",
			Title:       "Fetch Instagram profile details",
			Description: InstagramDetailsDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "instagram_detailsArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "urls",
						Title: "Urls",
						Description: "Profile URLs (or bare profile IDs). Provide urls OR " +
							"search_queries.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:  "search_queries",
						Title: "Search Queries",
						Description: "Terms to discover public profiles for. Google-backed " +
							"discovery is slow (~30-60s per query), so start with at most 3 " +
							"queries and expand only if nothing significant is found. Provide " +
							"search_queries OR urls.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					instagramSearchType(),
					toolschema.Parameter{
						Name:        "max_items",
						Title:       "Max Items",
						Description: "Max detail items to return.",
						Kind:        toolschema.Number,
						Default:     10,
						Minimum:     toolschema.Limit(1),
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "instagram", "details",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("search_queries", textList(arguments, "search_queries")),
						pythonjson.Field("search_type", pythonjson.Str(arguments.Text("search_type"))),
						pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					))
			},
		},
	}
}
