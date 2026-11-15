package modelconnections

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// endpointSource is the `discovery` word of the provider whose models are
// listed from the connection's own address (endpoint.go).
const endpointSource = "endpoint"

// There is one provider a connection may be made to: an OpenAI-compatible
// endpoint (ownmodels.Provider), called by this tier itself at the address
// and with the key the person gave.
//
// Why only one. The deployment's own models come through the model proxy,
// by name, and need no connection at all. A connection exists for a
// person's own key and address, and the only connections anything calls
// are OpenAI-compatible ones: the chat, the studio, the jobs and the vision
// checks open a chosen model through ownmodels, whose statements read
// `provider = 'openai_compatible'` and nothing else. The Python tier also
// offered OpenAI, Anthropic, Vertex AI, Bedrock, Azure, OpenRouter,
// Requesty, Ollama and LM Studio as providers of their own; in this tier a
// connection to any of them was stored and never called, so they are not
// offered. Those of them that speak OpenAI's protocol (OpenAI, Anthropic's
// and Gemini's compatibility endpoints, OpenRouter, Requesty, Ollama,
// LM Studio) are reached as what they are, an OpenAI-compatible endpoint:
// the web app's presets only fill in the base URL. Bedrock's, Azure's and
// Vertex AI's own protocols are spoken by nothing here.

// knownProvider reports whether a connection may name the provider.
func knownProvider(provider string) bool { return provider == ownmodels.Provider }

// providerUnknown is said of a body that names any other provider, null
// included: it tells the person what to send instead.
const providerUnknown = "Only an OpenAI-compatible connection can be added: give its base URL and key."

// BuildProviders is the provider list the settings screen draws its form
// from: one entry. The field names are the web app's contract
// (contracts/types/model-connections.types.ts), so they are kept although
// most now have one possible value.
func BuildProviders() []any {
	item := pythonjson.NewObject()
	item.Set("provider", ownmodels.Provider)
	item.Set("transport", "OPENAI_COMPATIBLE")
	// Listed from the endpoint's own GET {base}/models (endpoint.go).
	item.Set("discovery", endpointSource)
	// There is no one address to suggest: the person names the endpoint,
	// and the web app's presets fill in a known service's.
	item.Set("default_base_url", nil)
	item.Set("base_url_required", true)
	item.Set("auth_style", "bearer")
	// An address on the operator's own machine is reached only when the
	// operator put its host on the allow-list (ownmodels.Egress); it is not
	// a different kind of provider.
	item.Set("local_only", false)
	// Any account may connect one (write_routes.go, "Who may").
	item.Set("any_account", true)
	return []any{item}
}
