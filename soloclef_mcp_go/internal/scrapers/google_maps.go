package scrapers

// The Google Maps tools: places and reviews.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func googleMapsDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_google_maps_scrape",
			Title:       "Find places on Google Maps",
			Description: GoogleMapsScrapeDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "google_maps_scrapeArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "search_queries",
						Title: "Search Queries",
						Description: "Place searches, e.g. ['coffee shops']. Provide " +
							"search_queries OR urls OR place_ids.",
						Kind:     toolschema.TextList,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:        "urls",
						Title:       "Urls",
						Description: "Google Maps URLs of specific places.",
						Kind:        toolschema.TextList,
						Nullable:    true,
					},
					toolschema.Parameter{
						Name:        "place_ids",
						Title:       "Place Ids",
						Description: "Google place ids, e.g. ['ChIJj61dQgK6j4AR...'].",
						Kind:        toolschema.TextList,
						Nullable:    true,
					},
					toolschema.Parameter{
						Name:        "location",
						Title:       "Location",
						Description: "Geographic scope for a search, e.g. 'Seattle, USA'.",
						Kind:        toolschema.Text,
						Nullable:    true,
					},
					toolschema.Parameter{
						Name:        "max_places",
						Title:       "Max Places",
						Description: "Maximum places to return.",
						Kind:        toolschema.Number,
						Default:     10,
						Minimum:     toolschema.Limit(1),
					},
					toolschema.Parameter{
						Name:        "include_details",
						Title:       "Include Details",
						Description: "True adds opening hours and extra contact info (slower).",
						Kind:        toolschema.Flag,
						Default:     false,
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "google_maps", "scrape",
					pythonjson.NewObject(
						pythonjson.Field("search_queries", textList(arguments, "search_queries")),
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("place_ids", textList(arguments, "place_ids")),
						pythonjson.Field("location", optionalText(arguments, "location")),
						pythonjson.Field("max_places", pythonjson.Num(arguments.Number("max_places"))),
						pythonjson.Field("include_details", pythonjson.Flag(arguments.Flag("include_details"))),
					))
			},
		},
		{
			Name:        "soloclef_google_maps_reviews",
			Title:       "Fetch Google Maps reviews",
			Description: GoogleMapsReviewsDescription,
			Annotations: tooldefinition.ScrapeOpen,
			Signature: toolschema.Signature{
				ArgumentsTitle: "google_maps_reviewsArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:        "urls",
						Title:       "Urls",
						Description: "Google Maps URLs of places. Provide urls OR place_ids.",
						Kind:        toolschema.TextList,
						Nullable:    true,
					},
					toolschema.Parameter{
						Name:        "place_ids",
						Title:       "Place Ids",
						Description: "Google place ids from soloclef_google_maps_scrape.",
						Kind:        toolschema.TextList,
						Nullable:    true,
					},
					toolschema.Parameter{
						Name:        "max_reviews",
						Title:       "Max Reviews",
						Description: "Maximum reviews per place.",
						Kind:        toolschema.Number,
						Default:     20,
						Minimum:     toolschema.Limit(1),
					},
					toolschema.Parameter{
						Name:        "sort_by",
						Title:       "Sort By",
						Description: "Review ordering.",
						Kind:        toolschema.Text,
						Choices:     []string{"newest", "mostRelevant", "highestRanking", "lowestRanking"},
						Default:     "newest",
					},
					toolschema.Parameter{
						Name:        "language",
						Title:       "Language",
						Description: "Reviews language code, e.g. 'en'.",
						Kind:        toolschema.Text,
						Default:     "en",
					},
					toolschema.Parameter{
						Name:  "start_date",
						Title: "Start Date",
						Description: "ISO date like '2026-01-01'; keeps only reviews on " +
							"or after that day.",
						Kind:     toolschema.Text,
						Nullable: true,
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return runCapability(ctx, client, selection, arguments, "google_maps", "reviews",
					pythonjson.NewObject(
						pythonjson.Field("urls", textList(arguments, "urls")),
						pythonjson.Field("place_ids", textList(arguments, "place_ids")),
						pythonjson.Field("max_reviews", pythonjson.Num(arguments.Number("max_reviews"))),
						pythonjson.Field("sort_by", pythonjson.Str(arguments.Text("sort_by"))),
						pythonjson.Field("language", pythonjson.Str(arguments.Text("language"))),
						pythonjson.Field("start_date", optionalText(arguments, "start_date")),
					))
			},
		},
	}
}
