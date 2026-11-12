// Package nextcloudconnect connects an account to the deployment's
// Nextcloud with Nextcloud's own Login Flow v2, and disconnects it.
//
//	GET    /api/v1/nextcloud/connection                          the caller's connection, and the server it would use
//	POST   /api/v1/nextcloud/login                               start a sign-in: a login page to open
//	POST   /api/v1/nextcloud/login/{flow_id}/poll                has the person granted access yet?
//	DELETE /api/v1/nextcloud/login/{flow_id}                     give a sign-in up
//	DELETE /api/v1/connectors/{connector_id}/nextcloud/connection  disconnect, and end the app password at Nextcloud
//
// # How a connection is made
//
// The person never types an address or a password here. The server asks
// Nextcloud for a login page and a poll token, hands the browser the page
// and a random flow id, and keeps the token (sealed, in Valkey, for the
// twenty minutes Nextcloud keeps the flow). The browser polls with the
// flow id; the server polls Nextcloud with the token. When the person has
// granted access, Nextcloud hands over an app password, once. It is proven
// (who the account is, and that its files can be listed), sealed the way
// every stored credential is (soloclef_connectors/storedtoken) and written into the
// account's one Nextcloud connection. A password it replaces is ended at
// Nextcloud.
//
// # What keeps it safe
//
//   - One server: the deployment's (NEXTCLOUD_BASE_URL). The poll endpoint
//     Nextcloud names must be on it, or the token is not sent.
//   - A flow belongs to the account that started it. Another account's
//     flow id is answered exactly as an expired one.
//   - Every route is for a person in a session: connecting a store of
//     files is not a decision for an API key or an agent.
//   - No answer carries the poll token, the app password or its ciphertext.
//
// # What changed from the Python
//
//   - The connection is found and replaced with the account's row locked,
//     so two sign-ins finishing at once make one connection, not two.
//   - Disconnecting names the owner in the statement; another account's
//     connection is 404, not 403, so an id says nothing about who has one.
//   - No per-route attempt counters: the tier's own rate limit covers these
//     routes, and the only server they can be pointed at is the operator's.
package nextcloudconnect

// The paths.
const (
	ConnectionPath = "/api/v1/nextcloud/connection"
	LoginPath      = "/api/v1/nextcloud/login"
	PollPath       = "/api/v1/nextcloud/login/{flow_id}/poll"
	CancelPath     = "/api/v1/nextcloud/login/{flow_id}"
	DisconnectPath = "/api/v1/connectors/{connector_id}/nextcloud/connection"
)

// ServedRoutes is every route of this package as "METHOD path", for the
// lists cmd/api keeps. None reads a body.
func ServedRoutes() []string {
	return []string{
		"GET " + ConnectionPath,
		"POST " + LoginPath,
		"POST " + PollPath,
		"DELETE " + CancelPath,
		"DELETE " + DisconnectPath,
	}
}

// What the routes say, and the codes the web app tells them apart by.
const (
	sessionRequired = "This action requires an interactive session"

	notConfiguredCode = "nextcloud_not_configured"
	notConfigured     = "Nextcloud is not set up on this server"
	flowGoneCode      = "nextcloud_login_expired"
	flowGone          = "This Nextcloud sign-in expired; start again"
	unusableCode      = "nextcloud_login_unusable"
	unreachableCode   = "nextcloud_unreachable"
	connectionAbsent  = "Nextcloud connection not found"
)

// connectionName is what a new connection is called in the account's list.
const connectionName = "Nextcloud"
