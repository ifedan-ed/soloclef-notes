// Package modelconnections serves an account's own model connections: the
// one provider a connection can be made to, the account's connections with
// their models, and making, changing, removing and testing one.
//
//	GET /api/v1/model-providers     the provider (a constant; session only)
//	GET /api/v1/model-connections   the caller's connections and their models
//	the writes and the tests        write_routes.go, test_routes.go
//	a connection's models           model_routes.go (verify, list, switch, edit)
//
// # What a connection is for
//
// The deployment's own models are called through the model proxy, by a
// name the proxy lists (internal/chat/models.go, internal/modelchoices),
// and need no connection. A connection is the one exception to "every
// model goes through the proxy": a person's own OpenAI-compatible endpoint
// (ownmodels.Provider), with its base URL, its key and optional headers.
// Soloclef calls it itself: its models are listed from its own
// GET {base}/models, it is tested at its own POST {base}/chat/completions
// (endpoint.go), and its models join the account's model menus, where a
// chosen one answers the chat, the studio, the podcasts, the videos and the
// automations (soloclef_assistant/ownmodels). Any account may add one, and
// every call goes through the egress guard, so the address cannot reach
// inside this server's network unless the operator allowed that host.
//
// That is the only provider (providers.go says why). A hosted service that
// speaks OpenAI's protocol, such as OpenRouter or OpenAI itself, is added
// as an OpenAI-compatible endpoint at its address. A row of another
// provider left from before can be seen and removed, and nothing else
// (write_routes.go, "A row of a provider that is gone").
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
// holds ciphertext bound to its owner. The connection's headers are sealed
// the same way, whole (a header may carry a key as well as the key field
// does), and only their names are kept in the clear.
//
// **Neither is ever answered**, sealed or not. The answer says whether a
// key is saved (`has_api_key`) and which headers are sent (`header_names`),
// and nothing more (render.go, withoutCredentialBags). The settings screen
// does not need them back: an update that leaves `api_key` or `headers` out
// keeps what is stored, and POST /model-connections/{id}/test-update tests
// a change with it.
//
// **The key is read in one place here**: the statements a test or a
// listing runs (queries.sql, ConnectionForTest and ModelForTest), which
// hand it to the connection's own endpoint and to nothing else. (A chosen
// model is opened for a call by soloclef_assistant/ownmodels, its one other
// reader.) The list and the answer to a write ask the database for
// `has_api_key` as a boolean, so the key is not in memory on those paths to
// be logged, rendered or left behind by a later change.
//
// Two things follow and both are kept:
//
//   - nothing of a row is logged. No failure message, and no attribute of
//     any log line written here, carries one.
//   - no test holds a real credential. Every key a test stores, and every
//     credential inside an `extra`, is invented and written `fake...`, and
//     the tests refuse an answer in which one came back.
//
// # What is easy to read past
//
//   - `has_api_key` is whether a non-empty value is stored, so a removed
//     key is false.
//   - `extra` holding only keys that are never answered is answered `{}`.
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
