package websearch

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StoredKeys reads a provider's key from the account's connections.
//
// An account connects Exa or Tavily by adding that provider's MCP server,
// and the key it entered is in that connection's headers or address. So the
// key is found by looking at the account's MCP connections, oldest first,
// for one whose server is the provider's.
type StoredKeys struct {
	Pool *pgxpool.Pool
}

func (s StoredKeys) Key(ctx context.Context, userID string, provider Provider) (string, bool, error) {
	configs, err := New(s.Pool).SearchServerConfigs(ctx, userID)
	if err != nil {
		return "", false, err
	}
	for _, config := range configs {
		if key := keyFrom(config, provider); key != "" {
			return key, true, nil
		}
	}
	return "", false, nil
}

// hosts are the providers' own domains. A key is only taken from a
// connection whose server is on its provider's domain, so a connection to
// some other server that happens to carry an x-api-key header is never
// mistaken for an Exa key and sent to Exa.
var hosts = map[Provider]string{Exa: "exa.ai", Tavily: "tavily.com"}

// queryNames are the query parameters each provider's server takes a key in.
var queryNames = map[Provider][]string{
	Exa:    {"exaApiKey", "exa_api_key", "apiKey", "api_key"},
	Tavily: {"tavilyApiKey", "tavily_api_key", "apiKey", "api_key"},
}

// keyFrom returns the provider's key in one connection's config, or "".
// A config that cannot be read is a connection without a key, not an error:
// one malformed row must not take web search away from the account's
// other connections.
func keyFrom(config []byte, provider Provider) string {
	var stored struct {
		Server struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"server_config"`
	}
	if json.Unmarshal(config, &stored) != nil {
		return ""
	}
	address, err := url.Parse(stored.Server.URL)
	if err != nil {
		return ""
	}
	host, domain := strings.ToLower(address.Hostname()), hosts[provider]
	if host != domain && !strings.HasSuffix(host, "."+domain) {
		return ""
	}
	for name, value := range stored.Server.Headers {
		lowered := strings.ToLower(name)
		if provider == Exa && lowered == "x-api-key" && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	for name, value := range stored.Server.Headers {
		if strings.ToLower(name) != "authorization" {
			continue
		}
		if scheme, token, split := strings.Cut(strings.TrimSpace(value), " "); split && strings.EqualFold(scheme, "bearer") && strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token)
		}
	}
	query := address.Query()
	for _, name := range queryNames[provider] {
		if value := strings.TrimSpace(query.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
