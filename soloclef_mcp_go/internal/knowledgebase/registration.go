package knowledgebase

import (
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
)

// Definitions are every knowledge-base tool, reads before writes, in the order
// the Python server registers them.
func Definitions(client *restclient.Client) []tooldefinition.Definition {
	return append(
		readDefinitions(client),
		writeDefinitions(client)...,
	)
}
