package scrapers

// The Amazon product tool.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func amazonDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{{
		Name:        "soloclef_amazon_scrape",
		Title:       "Scrape Amazon products",
		Description: AmazonScrapeDescription,
		Annotations: tooldefinition.ScrapeOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "amazon_scrapeArguments",
			Parameters: parameters(
				toolschema.Parameter{
					Name:  "urls",
					Title: "Urls",
					Description: "Amazon product, search, category, best-seller, or short " +
						"(a.co / amzn.to) URLs. Provide urls OR search_terms.",
					Kind:     toolschema.TextList,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:  "search_terms",
					Title: "Search Terms",
					Description: "Search phrases run on the Amazon domain, e.g. " +
						"['wireless earbuds']. Provide search_terms OR urls.",
					Kind:     toolschema.TextList,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:        "max_items",
					Title:       "Max Items",
					Description: "Max products per search term or category/best-seller URL.",
					Kind:        toolschema.Number,
					Default:     10,
					Minimum:     toolschema.Limit(1),
					Maximum:     toolschema.Limit(100),
				},
				toolschema.Parameter{
					Name:  "domain",
					Title: "Domain",
					Description: "Amazon marketplace domain, e.g. 'www.amazon.com', " +
						"'www.amazon.co.uk'.",
					Kind:    toolschema.Text,
					Default: "www.amazon.com",
				},
				toolschema.Parameter{
					Name:  "include_details",
					Title: "Include Details",
					Description: "Fetch full product detail pages. False returns faster " +
						"card-only results from search/category listings.",
					Kind:    toolschema.Flag,
					Default: true,
				},
				toolschema.Parameter{
					Name:  "max_offers",
					Title: "Max Offers",
					Description: "Extra marketplace offers to fetch per product. " +
						"0 returns the featured offer only.",
					Kind:    toolschema.Number,
					Default: 0,
					Minimum: toolschema.Limit(0),
					Maximum: toolschema.Limit(100),
				},
				toolschema.Parameter{
					Name:  "include_sellers",
					Title: "Include Sellers",
					Description: "Enrich the featured product and each offer with the " +
						"seller's public profile summary.",
					Kind:    toolschema.Flag,
					Default: false,
				},
				toolschema.Parameter{
					Name:  "max_variants",
					Title: "Max Variants",
					Description: "Product variants (e.g. colors, sizes) to return as " +
						"separate results. 0 disables variant expansion.",
					Kind:    toolschema.Number,
					Default: 0,
					Minimum: toolschema.Limit(0),
					Maximum: toolschema.Limit(100),
				},
				toolschema.Parameter{
					Name:        "include_variant_prices",
					Title:       "Include Variant Prices",
					Description: "Attach per-variant prices (one extra request per variant).",
					Kind:        toolschema.Flag,
					Default:     false,
				},
				toolschema.Parameter{
					Name:  "country_code",
					Title: "Country Code",
					Description: "Two-letter delivery country for localized pricing, " +
						"e.g. 'us'.",
					Kind:     toolschema.Text,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:  "zip_code",
					Title: "Zip Code",
					Description: "Delivery ZIP/postal code for localized availability and " +
						"pricing, e.g. '10001'.",
					Kind:     toolschema.Text,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:        "language",
					Title:       "Language",
					Description: "Content language for the domain, e.g. 'en'.",
					Kind:        toolschema.Text,
					Nullable:    true,
				},
			),
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return runCapability(ctx, client, selection, arguments, "amazon", "scrape",
				pythonjson.NewObject(
					pythonjson.Field("urls", textList(arguments, "urls")),
					pythonjson.Field("search_terms", textList(arguments, "search_terms")),
					pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					pythonjson.Field("domain", pythonjson.Str(arguments.Text("domain"))),
					pythonjson.Field("include_details", pythonjson.Flag(arguments.Flag("include_details"))),
					pythonjson.Field("max_offers", pythonjson.Num(arguments.Number("max_offers"))),
					pythonjson.Field("include_sellers", pythonjson.Flag(arguments.Flag("include_sellers"))),
					pythonjson.Field("max_variants", pythonjson.Num(arguments.Number("max_variants"))),
					pythonjson.Field("include_variant_prices", pythonjson.Flag(arguments.Flag("include_variant_prices"))),
					pythonjson.Field("country_code", optionalText(arguments, "country_code")),
					pythonjson.Field("zip_code", optionalText(arguments, "zip_code")),
					pythonjson.Field("language", optionalText(arguments, "language")),
				))
		},
	}}
}
