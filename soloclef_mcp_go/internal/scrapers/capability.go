// Package scrapers gives one MCP tool per Soloclef Notes platform capability.
//
// Web crawl, Google Search, Reddit, YouTube, Instagram, TikTok, Google Maps,
// Indeed, Amazon and Walmart each get a tool that maps a natural-language
// request to the workspace's scraper, and two run-history tools list and fetch
// past runs, so a large result truncated inline can be retrieved in full later.
// Each platform is one file.
package scrapers

import (
	"context"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/rendering"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

// runCapability executes one scraper verb for the resolved workspace and renders
// its output.
//
// Nulls are dropped from the body: the scraper door reads an omitted field and
// a null field differently, so an optional parameter the caller left out must
// not be sent at all.
func runCapability(ctx context.Context, client *restclient.Client,
	selection *workspaceselection.Selection, arguments toolschema.Arguments,
	platform, verb string, payload pythonjson.Value) (string, error) {
	workspace, err := selection.Resolve(ctx, arguments.Text("workspace"))
	if err != nil {
		return "", err
	}
	reply, err := client.Do(ctx, restclient.Call{
		Method:   "POST",
		Path:     fmt.Sprintf("/workspaces/%d/scrapers/%s/%s", workspace.ID, platform, verb),
		JSONBody: pythonjson.WithoutNulls(payload),
	})
	if err != nil {
		return "", err
	}
	// Inline results are compacted — redundant fields dropped, long fields
	// excerpted — so every item survives the overall clip; the complete output
	// is stored server-side and retrievable with soloclef_get_scraper_run.
	compacted := rendering.CompactItems(reply, rendering.ItemFieldClipChars)
	if rendering.Chosen(arguments) == rendering.JSON {
		return rendering.Clip(rendering.ToJSON(compacted), rendering.DefaultClipChars), nil
	}
	return renderMarkdown(platform, verb, workspace.Name, compacted), nil
}

// renderMarkdown is a readable header plus the structured payload, clipped to a
// safe size.
func renderMarkdown(platform, verb, workspaceName string, result pythonjson.Value) string {
	header := fmt.Sprintf("# %s.%s — %s from \"%s\"",
		platform, verb, describeSize(result), workspaceName)
	body := rendering.Clip(rendering.ToJSON(result), rendering.DefaultClipChars)
	footer := "\n\nFields shown as excerpts; use soloclef_get_scraper_run for the " +
		"full output."
	return fmt.Sprintf("%s\n\n```json\n%s\n```%s", header, body, footer)
}

func describeSize(result pythonjson.Value) string {
	if result.Kind == pythonjson.Object {
		if items, ok := result.Lookup("items"); ok && items.Kind == pythonjson.Array {
			return fmt.Sprintf("%d item(s)", len(items.Array))
		}
	}
	return "result"
}

// workspaceAndFormat are the last two parameters of every scraper tool, in the
// order the Python handlers declare them.
func workspaceAndFormat() []toolschema.Parameter {
	return []toolschema.Parameter{
		workspaceselection.ReferenceParameter(),
		rendering.FormatParameter(),
	}
}

// parameters joins a tool's own parameters with the two every scraper tool ends
// with.
func parameters(own ...toolschema.Parameter) []toolschema.Parameter {
	return append(own, workspaceAndFormat()...)
}

// textList, optionalText and optionalNumber read a parameter into the payload
// value the scraper door expects, leaving an omitted one as null so
// runCapability drops it.
func textList(arguments toolschema.Arguments, name string) pythonjson.Value {
	return pythonjson.Strings(arguments.TextList(name))
}

func optionalText(arguments toolschema.Arguments, name string) pythonjson.Value {
	text, given := arguments.OptionalText(name)
	return pythonjson.OptionalStr(text, given)
}

func optionalNumber(arguments toolschema.Arguments, name string) pythonjson.Value {
	number, given := arguments.OptionalNumber(name)
	return pythonjson.OptionalNum(number, given)
}
