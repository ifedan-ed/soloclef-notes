package chat

import (
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// ConnectedAppSiteHosts is the hosts a site MCP server may be on while the
// operator has saved no list of their own: the same list the chat connects
// with (assembly.ConnectedAppSiteHosts). The settings routes that add and
// test a server are given it through here (cmd/api), so a server that can
// be saved is one the chat will call, and the list is written in one place.
func ConnectedAppSiteHosts() []string { return assembly.ConnectedAppSiteHosts() }

// ConnectedAppConnectTimeout is how long one connection to an MCP server
// may take to open, for the same routes and the same reason.
const ConnectedAppConnectTimeout = assembly.ConnectedAppConnectTimeout

// ConnectedAppOAuthTimeout is how long one request to an MCP server's
// authorization server may take: the sign-in routes are given the time a
// turn's token renewal is (assembly.ConnectedAppOAuthTimeout).
const ConnectedAppOAuthTimeout = assembly.ConnectedAppOAuthTimeout
