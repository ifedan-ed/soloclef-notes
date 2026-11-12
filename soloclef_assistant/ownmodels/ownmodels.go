// Package ownmodels is an account's own OpenAI-compatible model endpoints:
// the connections a person added with a base URL, a key and optional
// headers, and the models on them, which Soloclef calls directly.
//
// # The one exception to "every model goes through the proxy"
//
// The deployment's own models are reached only through the model proxy, by
// the names it lists. A connection a person adds is the exception, and the
// only one: its models are called at the address the person gave, with the
// key the person gave, to list them (GET {base}/models), to chat with them
// (POST {base}/chat/completions, soloclef_agent's openaicompat) and to show
// them pictures as an account's vision model (OpenVision, the same route
// through soloclef_ingest/vision).
// Nothing else in the deployment is reached this way, and a person cannot
// make the deployment call anything but what they themselves connected.
//
// # Naming a choice
//
// A choice of model is one string in `user_models.model` (and in an
// automation's steps). A proxy name is stored as the proxy spells it; a
// model of an account's own connection is stored as "own:<models.id>"
// (Choice, ParseChoice). Resolver is the one place that reads the string
// and answers which of the two it is, so every caller that opens the chosen
// model (the chat, the studio, the podcast and video jobs, the automations,
// the quick checks) opens it the same way; VisionResolver is the same rule
// for a vision choice (the indexer, the file tools, a built document's
// checks, a video's stills).
//
// The id is the models row's, not the provider's model name, because the
// row is what is the account's: the join from it names its owner, so an id
// of another account's model opens nothing (queries.sql). A chosen model
// whose row was removed is not offered, and the turn says so; no other
// model answers instead.
//
// # The one essential control: where a connection may go
//
// A person names the address, and this server makes the request, so the
// address must not be a way into this server's own network. Egress builds
// every client these connections use, and it refuses an address that
// resolves inside the network unless the operator put that host on the
// allow-list (site_settings `model_connection_allowed_hosts`, the mechanism
// `mcp_allowed_hosts` uses). The check is made at call time, on the
// connection itself, by the page reader's guard: one resolution, every
// address checked, the connection made to a checked address by number, so a
// name that answers differently a moment later (DNS rebinding) gains
// nothing. Redirects are not followed, so the key is never carried
// anywhere else.
//
// # The key and the headers
//
// Both are sealed before they are stored (KeySeal) and never answered. A
// header may carry a key as well as the key field does, so the headers are
// sealed whole and only their names are kept in the clear (headers.go).
package ownmodels

import (
	"strconv"
	"strings"
)

// Provider is the connection provider whose models are called directly:
// the "OpenAI-compatible" connection of the settings screen. Every other
// provider is reached through the model proxy as before.
const Provider = "openai_compatible"

// choicePrefix starts a stored choice that names a model of an account's
// own connection.
const choicePrefix = "own:"

// Choice is the stored name of a model of an account's own connection: the
// string the menus offer, a PUT sends and `user_models.model` holds.
func Choice(modelRowID int64) string {
	return choicePrefix + strconv.FormatInt(modelRowID, 10)
}

// ParseChoice reports whether name names a model of an account's own
// connection, and which row. Only the canonical spelling Choice writes is
// one ("own:42", not "own:042" or "own:+42"), so that one model has one
// name and two strings can never be the same choice.
func ParseChoice(name string) (int64, bool) {
	digits, found := strings.CutPrefix(name, choicePrefix)
	if !found {
		return 0, false
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != digits {
		return 0, false
	}
	return id, true
}

// IsChoiceSpelling reports whether a proxy name would be read as a choice
// of an own model. The menus leave such a proxy name out: offered, it
// would open the account's model and not the proxy's.
func IsChoiceSpelling(name string) bool {
	return strings.HasPrefix(name, choicePrefix)
}
