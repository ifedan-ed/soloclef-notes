package scrapers

import (
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

// Definitions are every scraper and run-history tool, in the order the Python
// server registers them.
func Definitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	slices := []func(*restclient.Client, *workspaceselection.Selection) []tooldefinition.Definition{
		webDefinitions,
		googleSearchDefinitions,
		redditDefinitions,
		youtubeDefinitions,
		instagramDefinitions,
		tiktokDefinitions,
		googleMapsDefinitions,
		indeedDefinitions,
		amazonDefinitions,
		walmartDefinitions,
		runHistoryDefinitions,
	}
	var definitions []tooldefinition.Definition
	for _, slice := range slices {
		definitions = append(definitions, slice(client, selection)...)
	}
	return definitions
}
