package knowledgebase

import (
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

// Definitions are every knowledge-base tool, reads before writes, in the order
// the Python server registers them.
func Definitions(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	return append(
		readDefinitions(client, selection),
		writeDefinitions(client, selection)...,
	)
}
