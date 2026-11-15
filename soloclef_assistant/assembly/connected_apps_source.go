package assembly

import "git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"

// ConnectedAppsOf is an MCP Source as the assembly's Connections: the
// adapter NewTools builds the production assembly with, for a caller that
// made its own Source. A test that runs a real MCP server against a real
// Source needs it, so that what it checks is the adapter a deployment runs
// and not a copy of it.
func ConnectedAppsOf(source *mcptools.Source) Connections {
	return connectedApps{source}
}
