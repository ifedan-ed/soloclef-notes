// Package modelconnections serves the list of providers a connection can be
// made to, and an account's own connections with their models: reading
// them, and making, changing, removing and testing one.
//
//	GET /api/v1/model-providers     the providers (a constant; session only)
//	GET /api/v1/model-connections   the caller's connections and their models
//	the writes and the tests        write_routes.go
//	a connection's models           model_routes.go (verify, list, switch, edit)
//	the site's own connections      global_routes.go
//
// A connection is an account's own: the key it holds is that person's key.
// Every statement is `WHERE user_id = caller`, so one account never sees or
// changes another's connection, and the operator's connections (scope
// "global", no owner) are neither listed nor writable here.
//
// # The key
//
// This is the part to read before changing anything here.
//
// The Python tier stored a provider's API key as it was typed, in a plain
// column. This tier seals it before storing it (keyseal.go), so the column
// holds ciphertext bound to its owner.
//
// **The key is never answered**, sealed or not. The answer says whether a
// key is saved (`has_api_key`) and nothing more. The settings screen does
// not need it back: an update that leaves `api_key` out keeps the stored
// key, and POST /model-connections/{id}/test-update tests a change with it.
//
// The same goes for `extra`. For Amazon Bedrock and Vertex AI the
// credential is not in `api_key` at all: the access key, secret, bearer
// token or whole service-account file sits in `extra.litellm_params`. The
// answer therefore leaves `litellm_params` and `kwargs` out (render.go,
// withoutCredentialBags). Those two bags are still stored as sent; sealing
// them as the key is sealed is not done yet.
//
// **The key is read in one place**: the statements a test runs
// (queries.sql, ConnectionForTest and ModelForTest), which hand it to the
// model proxy, or to the OpenAI-compatible endpoint it belongs to, and to
// nothing else. (A chosen model of such a connection is opened for a call
// by soloclef_assistant/ownmodels, its one other reader.) The list and the answer to a write ask
// the database for `has_api_key` as a boolean, so the key is not in memory
// on those paths to be logged, rendered or left behind by a later change.
//
// Two things follow and both are kept:
//
//   - nothing of a row is logged. No failure message, and no attribute of
//     any log line written here, carries one.
//   - no test holds a real credential. Every key a test stores, and every
//     credential inside an `extra`, is invented and written `fake...`, and
//     the tests refuse an answer in which one came back.
//
// # What a connection is for
//
// The deployment's own models are called through the model proxy, by a
// name the proxy lists (internal/chat/models.go, internal/modelchoices).
// A connection is a person's own key and address, and there are two kinds.
//
// An OpenAI-compatible connection (ownmodels.Provider) is called by
// Soloclef itself, at the address the person gave: its models are listed
// from its own GET {base}/models, it is tested there (endpoint.go), and its
// models join the account's model menus, where a chosen one answers the
// chat, the studio, the podcasts, the videos and the automations
// (soloclef_assistant/ownmodels). Any account may add one; every call goes
// through the egress guard, so the address cannot reach inside this
// server's network unless the operator allowed that host.
//
// Every other provider's connection is handed to the proxy: its models are
// the proxy's catalogue (proxy_catalogue.go) and its test asks through the
// proxy (proxy_probe.go). Those remain an administrator's (write_routes.go,
// "Who may").
//
// The key, and an OpenAI-compatible connection's headers, are sealed at
// rest and never answered; `header_names` says which headers are sent.
//
// # What is easy to read past
//
//   - `has_api_key` is whether a non-empty value is stored, so a removed
//     key is false.
//   - `extra` holding only the two credential bags is answered `{}`.
//   - A jsonb column that should hold an object and does not fails the whole
//     list with the sanitised 500: a row this package cannot describe
//     truthfully is not described.
package modelconnections

// The two paths.
const (
	ProvidersPath   = "/api/v1/model-providers"
	ConnectionsPath = "/api/v1/model-connections"
)

// sessionRequired refuses an API key or an agent on the provider list, which
// is for the settings screen only.
const sessionRequired = "This action requires an interactive session"
