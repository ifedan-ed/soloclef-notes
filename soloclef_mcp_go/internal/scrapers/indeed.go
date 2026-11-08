package scrapers

// The Indeed tool.

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func indeedDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{{
		Name:        "soloclef_indeed_scrape",
		Title:       "Search or scrape Indeed jobs",
		Description: IndeedScrapeDescription,
		Annotations: tooldefinition.ScrapeOpen,
		Signature: toolschema.Signature{
			ArgumentsTitle: "indeed_scrapeArguments",
			Parameters: parameters(
				toolschema.Parameter{
					Name:  "urls",
					Title: "Urls",
					Description: "Indeed URLs: a search page " +
						"('https://www.indeed.com/jobs?q=data+analyst'), a company jobs " +
						"page ('/cmp/<slug>/jobs'), or a single job ('/viewjob?jk=...'). " +
						"Provide urls OR search_queries.",
					Kind:     toolschema.TextList,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:  "search_queries",
					Title: "Search Queries",
					Description: "Job search terms, e.g. ['data analyst', 'ml engineer']. " +
						"Provide search_queries OR urls.",
					Kind:     toolschema.TextList,
					Nullable: true,
				},
				toolschema.Parameter{
					Name:        "country",
					Title:       "Country",
					Description: "Country code selecting the Indeed domain, e.g. 'us', 'gb'.",
					Kind:        toolschema.Text,
					Default:     "us",
				},
				toolschema.Parameter{
					Name:        "location",
					Title:       "Location",
					Description: "Where to search, e.g. 'Remote', 'New York, NY'.",
					Kind:        toolschema.Text,
					Nullable:    true,
				},
				toolschema.Parameter{
					Name:        "radius",
					Title:       "Radius",
					Description: "Search radius in miles/km around location.",
					Kind:        toolschema.Number,
					Nullable:    true,
				},
				toolschema.Parameter{
					Name:        "job_type",
					Title:       "Job Type",
					Description: "Employment type filter.",
					Kind:        toolschema.Text,
					Choices: []string{"fulltime", "parttime", "contract", "internship",
						"temporary", "permanent", "seasonal", "freelance"},
					Nullable: true,
				},
				toolschema.Parameter{
					Name:        "level",
					Title:       "Level",
					Description: "Experience level filter.",
					Kind:        toolschema.Text,
					Choices:     []string{"entry_level", "mid_level", "senior_level"},
					Nullable:    true,
				},
				toolschema.Parameter{
					Name:        "remote",
					Title:       "Remote",
					Description: "Work model filter: remote or hybrid.",
					Kind:        toolschema.Text,
					Choices:     []string{"remote", "hybrid"},
					Nullable:    true,
				},
				toolschema.Parameter{
					Name:        "from_days",
					Title:       "From Days",
					Description: "Only return jobs posted within the last N days.",
					Kind:        toolschema.Number,
					Nullable:    true,
				},
				toolschema.Parameter{
					Name:        "sort",
					Title:       "Sort",
					Description: "Result ordering: relevance or date.",
					Kind:        toolschema.Text,
					Choices:     []string{"relevance", "date"},
					Default:     "relevance",
				},
				toolschema.Parameter{
					Name:  "scrape_job_details",
					Title: "Scrape Job Details",
					Description: "True fetches each job's detail page for the full " +
						"description (slower); False returns the listing snippet only.",
					Kind:    toolschema.Flag,
					Default: false,
				},
				maxItems("Maximum jobs to return in total.", 25),
				toolschema.Parameter{
					Name:        "max_items_per_query",
					Title:       "Max Items Per Query",
					Description: "Max jobs per search/company target.",
					Kind:        toolschema.Number,
					Default:     25,
					Minimum:     toolschema.Limit(0),
				},
			),
		},
		Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
			return runCapability(ctx, client, selection, arguments, "indeed", "scrape",
				pythonjson.NewObject(
					pythonjson.Field("urls", textList(arguments, "urls")),
					pythonjson.Field("search_queries", textList(arguments, "search_queries")),
					pythonjson.Field("country", pythonjson.Str(arguments.Text("country"))),
					pythonjson.Field("location", optionalText(arguments, "location")),
					pythonjson.Field("radius", optionalNumber(arguments, "radius")),
					pythonjson.Field("job_type", optionalText(arguments, "job_type")),
					pythonjson.Field("level", optionalText(arguments, "level")),
					pythonjson.Field("remote", optionalText(arguments, "remote")),
					pythonjson.Field("from_days", optionalNumber(arguments, "from_days")),
					pythonjson.Field("sort", pythonjson.Str(arguments.Text("sort"))),
					pythonjson.Field("scrape_job_details", pythonjson.Flag(arguments.Flag("scrape_job_details"))),
					pythonjson.Field("max_items", pythonjson.Num(arguments.Number("max_items"))),
					pythonjson.Field("max_items_per_query", pythonjson.Num(arguments.Number("max_items_per_query"))),
				))
		},
	}}
}
