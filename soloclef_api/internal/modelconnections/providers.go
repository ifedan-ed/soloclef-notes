package modelconnections

import (
	"sort"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// endpointSource is the `discovery` word of the provider whose models are
// listed from the connection's own address.
const endpointSource = "endpoint"

// providerSpec is the part of app/services/provider_registry.py's ProviderSpec
// that list_model_providers answers with. `litellm_prefix` and `display_name`
// are not here because the route does not send them.
type providerSpec struct {
	provider string
	// transport is Transport's value, which is its name.
	transport string
	// defaultBaseURL is nil for the Python None.
	defaultBaseURL  *string
	baseURLRequired bool
	authStyle       string
}

// proxyPrefixes is each provider's `litellm_prefix`: what the model proxy
// calls the provider in a model name ("openai/gpt-x"). Several providers
// speak OpenAI's protocol at their own address and share its prefix.
var proxyPrefixes = map[string]string{
	"openai": "openai", "anthropic": "anthropic", "azure": "azure", "vertex_ai": "vertex_ai",
	"bedrock": "bedrock", "openrouter": "openrouter", "requesty": "openai",
	"openai_compatible": "openai", "openai_compatible_raw": "openai", "lm_studio": "openai",
	"ollama_chat": "ollama_chat",
}

// proxyPrefix is the prefix of a provider a connection may be made to
// (knownProvider); every one of them has one.
func proxyPrefix(provider string) string { return proxyPrefixes[provider] }

// knownProvider reports whether a connection may name the provider: the
// registry's keys, the hidden one included so an old connection can still
// be saved.
func knownProvider(provider string) bool {
	_, known := proxyPrefixes[provider]
	return known
}

func address(text string) *string { return &text }

// registry is REGISTRY, entry for entry and **in that file's own order**, so
// that the two can be read side by side. It includes the deprecated entry the
// route hides; the hiding and the sorting are done below, as the route does
// them, rather than by leaving a line out here.
//
// The Python registry also named, per provider, a way of asking the provider
// itself for its models. That column is gone. What the route answers as
// `discovery` is where this tier lists a provider's models: the endpoint's
// own list for an OpenAI-compatible connection (endpoint.go), the model
// proxy's catalogue for the providers it covers (proxy_catalogue.go), or
// nowhere.
var registry = []providerSpec{
	{"openai", "NATIVE", nil, false, "bearer"},
	{"anthropic", "NATIVE", nil, false, "x-api-key"},
	{"azure", "NATIVE", nil, true, "native"},
	{"vertex_ai", "NATIVE", nil, false, "native"},
	{"bedrock", "NATIVE", nil, false, "native"},
	{"openrouter", "OPENAI_COMPATIBLE", address("https://openrouter.ai/api/v1"), false, "bearer"},
	{"requesty", "OPENAI_COMPATIBLE", address("https://router.requesty.ai/v1"), false, "bearer"},
	{"openai_compatible", "OPENAI_COMPATIBLE", nil, true, "bearer"},
	{"openai_compatible_raw", "NATIVE", nil, true, "bearer"},
	{"lm_studio", "OPENAI_COMPATIBLE", address("http://host.docker.internal:1234/v1"), true, "bearer"},
	{"ollama_chat", "OLLAMA", address("http://host.docker.internal:11434"), true, "none"},
}

// The two sets list_model_providers builds on each call.
var (
	// localOnly is `local_only`: the providers that run on the operator's own
	// machine.
	localOnly = map[string]bool{"ollama_chat": true, "lm_studio": true}
	// hiddenProviders is `hidden`: deprecated, kept in the registry so that an
	// existing connection still resolves, and not offered for a new one.
	hiddenProviders = map[string]bool{"openai_compatible_raw": true}
)

// BuildProviders is the list comprehension list_model_providers returns.
//
// `sorted(REGISTRY.items())` orders by the provider's name, and Python orders
// two strings by code point. Go orders them by byte, which is the same order
// for UTF-8 -- and these names are ASCII in any case.
func BuildProviders() []any {
	offered := make([]providerSpec, 0, len(registry))
	for _, spec := range registry {
		if hiddenProviders[spec.provider] {
			continue
		}
		offered = append(offered, spec)
	}
	sort.Slice(offered, func(i, j int) bool { return offered[i].provider < offered[j].provider })

	items := make([]any, 0, len(offered))
	for _, spec := range offered {
		// ModelProviderRead's fields, in the order the model declares them.
		item := pythonjson.NewObject()
		item.Set("provider", spec.provider)
		item.Set("transport", spec.transport)
		// Where the provider's models are listed from: the model proxy's
		// catalogue, or nowhere (proxy_catalogue.go). The Python tier named
		// a way of asking each provider directly; this tier asks none.
		switch {
		case spec.provider == ownmodels.Provider:
			// Listed from the endpoint's own GET {base}/models
			// (endpoint.go): the one provider this tier asks itself.
			item.Set("discovery", endpointSource)
		case HasCatalogue(spec.provider):
			item.Set("discovery", catalogueSource)
		default:
			item.Set("discovery", noCatalogue)
		}
		if spec.defaultBaseURL == nil {
			item.Set("default_base_url", nil)
		} else {
			item.Set("default_base_url", *spec.defaultBaseURL)
		}
		item.Set("base_url_required", spec.baseURLRequired)
		item.Set("auth_style", spec.authStyle)
		item.Set("local_only", localOnly[spec.provider])
		// Whether any account may connect it, or an administrator only
		// (write_routes.go, "Who may").
		item.Set("any_account", spec.provider == ownmodels.Provider)
		items = append(items, item)
	}
	return items
}
