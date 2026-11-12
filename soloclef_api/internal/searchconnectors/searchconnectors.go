// Package searchconnectors serves an account's connections: its Drive, its
// Nextcloud, its mailboxes and its MCP servers.
//
//	GET    /api/v1/search-source-connectors                          the caller's connections, a page
//	POST   /api/v1/search-source-connectors                          make a web-pages connection
//	GET    /api/v1/search-source-connectors/{connector_id}           one of them
//	PUT    /api/v1/search-source-connectors/{connector_id}           change one's name, settings and schedule
//	PUT    /api/v1/search-source-connectors/{connector_id}/enabled   switch one on or off
//	DELETE /api/v1/search-source-connectors/{connector_id}           remove one, and its documents
//	GET    /api/v1/connectors/mcp                                    the caller's MCP servers
//	POST   /api/v1/connectors/mcp                                    add one
//	GET    /api/v1/connectors/mcp/policy                             may the caller add one, and under which rule
//	POST   /api/v1/connectors/mcp/test                               connect to one before saving it
//	GET    /api/v1/connectors/mcp/{connector_id}                     one of them
//	PUT    /api/v1/connectors/mcp/{connector_id}                     change one's name or settings
//	DELETE /api/v1/connectors/mcp/{connector_id}                     remove one
//	POST   /api/v1/connectors/mcp/{connector_id}/trust-tool          let a tool run without asking
//	POST   /api/v1/connectors/mcp/{connector_id}/untrust-tool        make it ask again
//
// # Whose a connection is
//
// A connection belongs to the account whose credentials it holds, and to
// nobody else. Every statement of this package names that owner
// (queries.sql), so a connection of another account is answered exactly as
// one that does not exist: 404, from the same line of code. There is no
// membership to check, no permission to hold and no second table of
// per-place switches: whether a connection is in use is its own `enabled`
// column, which the chat and the indexing schedule both read.
//
// # What a connection is, and where its credential lives
//
// A row of `search_source_connectors` is one connection: a Google Drive
// grant, a Nextcloud login, a set of IMAP mailboxes, or an MCP server. Its
// credential is in the row's `config` column, a `jsonb` document, and how it
// is held there depends on the type:
//
//   - an MCP server's key is stored **in the clear**, in
//     `server_config.headers` or in `server_config.url`. Every read masks it
//     (secrets.go), for every caller, the person who typed it included.
//   - a mailbox's password or Microsoft token is Fernet ciphertext under
//     `accounts[]`. Every read reduces each account to nine public fields
//     (mailboxes.go), so not even the ciphertext leaves.
//   - a Drive grant's tokens and client secret, and a Nextcloud app password,
//     are Fernet ciphertext too. They are not returned either: a ciphertext
//     copied out of a response could be written into another connection,
//     where the server would decrypt it.
//
// What is returned is decided once, for every type, in public_config.go: a
// read shows the keys named there for the connection's type and nothing
// else, and the mask and the mailbox reduction are applied to what is left.
// The tests store invented credentials and refuse an answer in which one
// came back.
//
// Nothing in the reads decrypts, encrypts or compares a credential. The
// mask is string work and the reductions are a choice of keys. The one
// route that opens a stored credential is the connection's removal, and it
// does not do it here: it hands the stored settings to a Revoker
// (internal/connectionrevocation), which opens the token only to end it at
// its provider.
//
// # The writes, and what is not here
//
// The generic create and update are in connection_write_routes.go, which
// says what a request may name; an MCP server's own writes and its test are
// in server_write_routes.go and server_test_route.go, over the one reading
// of a server's settings in server_settings.go (strict, masked keys put
// back, the destination checked with the guard the chat calls it through).
// A connection that holds a sign-in is made by its own package
// (internal/driveconnect, internal/nextcloudconnect, internal/mailaccounts).
// Starting an index run (POST …/{connector_id}/index) is not served: there
// is no worker job for it to publish to yet.
//
// # Every change to an account's MCP servers moves a counter
//
// The chat caches MCP tool definitions per account and drops them when a
// counter in Valkey moves (soloclef_assistant/mcptools). Adding a server, editing
// one, removing one, switching one on or off and editing its trusted tools
// each move the account's counter after the change commits (tool_changes.go, which also
// says what happens when it cannot). What a call may do is read from the row
// at each call, so a cached definition is never acted on with a stale
// address, key or trusted list.
//
// # What is easy to read past
//
//   - A read validates the row. A config that is not an object, a mailbox
//     entry without its marker, and periodic indexing switched on for a row
//     that is not indexable are each a 500 on a GET, and one such row fails
//     the whole page: a row that cannot be shown truthfully is not shown.
//   - The trust routes are for a browser session only, and that is checked
//     before any row is read (trust_route.go).
//   - Each list is ordered by name, then id (queries.sql).
package searchconnectors

// The paths.
const (
	ListPath    = "/api/v1/search-source-connectors"
	OnePath     = "/api/v1/search-source-connectors/{connector_id}"
	EnabledPath = "/api/v1/search-source-connectors/{connector_id}/enabled"
	ServersPath = "/api/v1/connectors/mcp"
	ServerPath  = "/api/v1/connectors/mcp/{connector_id}"
	// The two literals beside ServerPath. ServeMux prefers a literal segment
	// to a wildcard, so neither is ever read as a server's id.
	ServerTestPath   = "/api/v1/connectors/mcp/test"
	ServerPolicyPath = "/api/v1/connectors/mcp/policy"
	TrustPath        = "/api/v1/connectors/mcp/{connector_id}/trust-tool"
	UntrustPath      = "/api/v1/connectors/mcp/{connector_id}/untrust-tool"
	serverTypeTag    = "MCP_CONNECTOR"
	mailboxType      = "EMAIL_IMAP_CONNECTOR"
)

// What the routes say.
const (
	connectorAbsent = "Connector not found"
	serverAbsent    = "MCP connector not found"
	toolAlwaysAsk   = "Tools that open web addresses always ask before running"
	sessionRequired = "This action requires an interactive session"
	toolUnstorable  = "This tool name contains a character that cannot be saved"
)

// BodyRoutes is every route of this package that reads a small JSON body
// through the buffered reader, as "METHOD path". cmd/api adds them to the
// routes whose bodies it buffers, and the tests mount the same list, so a
// route cannot be registered and left out of it unnoticed: its body would
// never reach it.
func BodyRoutes() []string {
	return []string{
		"POST " + TrustPath,
		"POST " + UntrustPath,
		"PUT " + EnabledPath,
		"POST " + ServersPath,
		"PUT " + ServerPath,
		"POST " + ServerTestPath,
		"POST " + ListPath,
		"PUT " + OnePath,
	}
}
