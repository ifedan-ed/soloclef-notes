package scrapers

// Scraper run history: list past runs and fetch one in full.
//
// A scrape whose inline result was truncated is retrievable here by run id, so
// the model never re-runs a scraper just to recover output.

import (
	"context"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/rendering"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

func runHistoryDefinitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_list_scraper_runs",
			Title:       "List past scraper runs",
			Description: ListScraperRunsDescription,
			Annotations: tooldefinition.ReadOnlyClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "list_scraper_runsArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:        "limit",
						Title:       "Limit",
						Description: "Maximum runs to list.",
						Kind:        toolschema.Number,
						Default:     20,
						Minimum:     toolschema.Limit(1),
					},
					toolschema.Parameter{
						Name:  "capability",
						Title: "Capability",
						Description: "Filter by capability slug, e.g. 'web.crawl' or " +
							"'reddit.scrape'.",
						Kind:     toolschema.Text,
						Nullable: true,
					},
					toolschema.Parameter{
						Name:        "status",
						Title:       "Status",
						Description: "Filter by run status: 'success' or 'error'.",
						Kind:        toolschema.Text,
						Nullable:    true,
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return listRuns(ctx, client, selection, arguments)
			},
		},
		{
			Name:        "soloclef_get_scraper_run",
			Title:       "Fetch one scraper run in full",
			Description: GetScraperRunDescription,
			Annotations: tooldefinition.ReadOnlyClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "get_scraper_runArguments",
				Parameters: parameters(
					toolschema.Parameter{
						Name:  "run_id",
						Title: "Run Id",
						Description: "Run id from soloclef_list_scraper_runs or a " +
							"prior scrape's output.",
						Kind:     toolschema.Text,
						Required: true,
					},
				),
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return getRun(ctx, client, selection, arguments)
			},
		},
	}
}

func listRuns(ctx context.Context, client *restclient.Client,
	selection *workspaceselection.Selection, arguments toolschema.Arguments) (string, error) {
	workspace, err := selection.Resolve(ctx, arguments.Text("workspace"))
	if err != nil {
		return "", err
	}
	capability, capabilityGiven := arguments.OptionalText("capability")
	status, statusGiven := arguments.OptionalText("status")
	runs, err := client.Do(ctx, restclient.Call{
		Method: "GET",
		Path:   fmt.Sprintf("/workspaces/%d/scrapers/runs", workspace.ID),
		Query: []restclient.QueryParameter{
			restclient.Number("limit", arguments.Number("limit")),
			queryIfGiven("capability", capability, capabilityGiven),
			queryIfGiven("status", status, statusGiven),
		},
	})
	if err != nil {
		return "", err
	}
	if rendering.Chosen(arguments) == rendering.JSON {
		return rendering.ToJSON(runs), nil
	}
	return renderRuns(runs), nil
}

func getRun(ctx context.Context, client *restclient.Client,
	selection *workspaceselection.Selection, arguments toolschema.Arguments) (string, error) {
	workspace, err := selection.Resolve(ctx, arguments.Text("workspace"))
	if err != nil {
		return "", err
	}
	runID := arguments.Text("run_id")
	run, err := client.Do(ctx, restclient.Call{
		Method: "GET",
		Path:   fmt.Sprintf("/workspaces/%d/scrapers/runs/%s", workspace.ID, runID),
	})
	if err != nil {
		return "", err
	}
	rendered := rendering.Clip(rendering.ToJSON(run), rendering.DefaultClipChars)
	if rendering.Chosen(arguments) == rendering.JSON {
		return rendered, nil
	}
	return fmt.Sprintf("# Run %s\n\n```json\n%s\n```",
		pythonjson.Text(run, "id", runID), rendered), nil
}

// queryIfGiven leaves a parameter out of the request when the caller omitted
// it, because sending it empty makes the API read "" as a value.
func queryIfGiven(name, value string, given bool) restclient.QueryParameter {
	if !given {
		return restclient.Omitted(name)
	}
	return restclient.Text(name, value)
}

func renderRuns(runs pythonjson.Value) string {
	if runs.Kind != pythonjson.Array || len(runs.Array) == 0 {
		return "No scraper runs found."
	}
	text := &rendering.Text{}
	text.Raw("# Scraper runs")
	text.Raw("")
	for _, run := range runs.Array {
		text.Line("- **%s** — %s · %s · %s item(s) · %s",
			pythonjson.Text(run, "id", "None"),
			pythonjson.Text(run, "capability", "None"),
			pythonjson.Text(run, "status", "None"),
			pythonjson.Text(run, "item_count", "0"),
			pythonjson.Text(run, "created_at", "None"))
	}
	return text.Joined()
}
