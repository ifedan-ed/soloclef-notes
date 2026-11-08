package scrapers

// The Walmart tools: products/listings and deep reviews.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func walmartDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_walmart_scrape",
			Title:       "Scrape Walmart products",
			Description: WalmartScrapeDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "walmart_scrapeArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "urls",
						Title: "Urls",
						Description: "Walmart product (/ip/), search (/search), category " +
							"(/cp/), or browse (/browse/) URLs. Provide urls OR search_terms.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:  "search_terms",
						Title: "Search Terms",
						Description: "Search phrases run on walmart.com, e.g. ['air fryer']. " +
							"Provide search_terms OR urls.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:        "max_items",
						Title:       "Max Items",
						Description: "Max products per search term or listing URL.",
						Kind:        toolschema.Number,
						Default:     10,
						Minimum:     toolschema.Limit(1),
						Maximum:     toolschema.Limit(100),
					},
					toolschema.Parameter{
						Name:  "include_details",
						Title: "Include Details",
						Description: "Fetch full product detail pages. False returns faster " +
							"card-only results from listings.",
						Kind:    toolschema.Flag,
						Default: true,
					},
					toolschema.Parameter{
						Name:  "include_reviews_sample",
						Title: "Include Reviews Sample",
						Description: "Include the free on-page review sample (rating " +
							"distribution, aspects, top reviews) on detail pages.",
						Kind:    toolschema.Flag,
						Default: true,
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "walmart", "scrape",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("search_terms", textList(arguments, "search_terms")),
						pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
						pythonjson.Field("include_details", pythonjson.Flag(arguments.Flag("include_details"))),
						pythonjson.Field("include_reviews_sample", pythonjson.Flag(arguments.Flag("include_reviews_sample"))),
					))
			},
		},
		{
			Name:        "soloclef_walmart_reviews",
			Title:       "Fetch Walmart reviews",
			Description: WalmartReviewsDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "walmart_reviewsArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:        "urls",
						Title:       "Urls",
						Description: "Walmart product URLs (/ip/...). Provide urls OR item_ids.",
						Kind:        toolschema.TextList,
						Nullable:    true,
					},
					toolschema.Parameter{
						Name:  "item_ids",
						Title: "Item Ids",
						Description: "Walmart numeric item ids (usItemId) from " +
							"soloclef_walmart_scrape.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:        "max_reviews",
						Title:       "Max Reviews",
						Description: "Max reviews per product (10 per page).",
						Kind:        toolschema.Number,
						Default:     200,
						Minimum:     toolschema.Limit(1),
						Maximum:     toolschema.Limit(5000),
					},
					toolschema.Parameter{
						Name:        "sort_by",
						Title:       "Sort By",
						Description: "Review ordering.",
						Kind:        toolschema.Text,
						Choices:     []string{"most-recent", "most-helpful", "rating-high", "rating-low"},
						Default:     "most-recent",
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "walmart", "reviews",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("item_ids", textList(arguments, "item_ids")),
						pythonjson.Field("max_reviews", pythonjson.Num(arguments.Number("max_reviews"))),
						pythonjson.Field("sort_by", pythonjson.Str(arguments.Text("sort_by"))),
					))
			},
		},
	}
}
