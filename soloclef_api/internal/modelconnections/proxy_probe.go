package modelconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Probe is one model of one connection, to be tried.
type Probe struct {
	Provider string
	// BaseURL and Key are empty when the connection has none.
	BaseURL string
	Key     string
	// Params is the connection's `extra.litellm_params` bag as a JSON
	// object, or nil: the credentials of a provider that has no single key
	// (Amazon Bedrock, Vertex AI).
	Params json.RawMessage
	// Headers are an OpenAI-compatible connection's own request headers,
	// opened: sent to the endpoint itself (endpoint.go), never to the proxy.
	Headers map[string]string
	ModelID string
}

// Verdict is what a test answers, in the words the settings screen
// branches on: "OK", "AUTH_FAILED", "NOT_FOUND" or "UNREACHABLE".
type Verdict struct {
	Status  string `json:"status"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// Prober tries a model. ProxyProber is the one a server runs with.
type Prober interface {
	Try(ctx context.Context, probe Probe) Verdict
}

// ProxyProber tries a model through the model proxy, which is the only
// way this tier reaches any model. The Python tier called the provider
// itself, from inside the API process, with a library that knew every
// provider's wire format; this tier has no such library and wants none.
//
// The proxy is sent an ordinary chat request for "<provider>/<model>" that
// carries the connection's own key and address, which LiteLLM accepts when
// its operator allows credentials from the caller. When it does not, the
// proxy says so, and that sentence is the verdict: a connection the proxy
// will not use is one this deployment cannot use, and the test's job is to
// say that before a person relies on it.
type ProxyProber struct {
	// URL is the proxy's OpenAI-compatible root, ending in /v1, and Key
	// the deployment's key for it.
	URL string
	Key string
	// HTTP makes the request; Timeout bounds it. A person is waiting on a
	// settings page.
	HTTP    *http.Client
	Timeout time.Duration
}

// Check reports what is missing. Called once at start-up.
func (p ProxyProber) Check() error {
	switch {
	case strings.TrimSpace(p.URL) == "":
		return errors.New("modelconnections: the model proxy's URL is not set")
	case p.Key == "":
		return errors.New("modelconnections: the model proxy's key is not set")
	case p.HTTP == nil:
		return errors.New("modelconnections: the HTTP client is not set")
	case p.Timeout <= 0:
		return errors.New("modelconnections: the test timeout must be positive")
	}
	return nil
}

// reservedParams are the request fields a connection's bag may not set:
// the ones this file sets itself.
var reservedParams = map[string]bool{"model": true, "messages": true, "stream": true, "api_key": true, "api_base": true}

// maxVerdictBody bounds what is read of the proxy's answer; maxMessage
// bounds what of it is shown.
const (
	maxVerdictBody = 64 << 10
	maxMessage     = 300
)

// Try sends one short question and reads how it went.
func (p ProxyProber) Try(ctx context.Context, probe Probe) Verdict {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	body := map[string]any{}
	if len(probe.Params) > 0 {
		var bag map[string]any
		if err := json.Unmarshal(probe.Params, &bag); err == nil {
			for name, value := range bag {
				if !reservedParams[name] {
					body[name] = value
				}
			}
		}
	}
	body["model"] = proxyPrefix(probe.Provider) + "/" + probe.ModelID
	body["messages"] = []map[string]string{{"role": "user", "content": "Hello"}}
	if probe.Key != "" {
		body["api_key"] = probe.Key
	}
	if probe.BaseURL != "" {
		body["api_base"] = probe.BaseURL
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return Verdict{Status: "UNREACHABLE", Message: "The test request could not be built."}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.URL, "/")+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return Verdict{Status: "UNREACHABLE", Message: "The test request could not be built."}
	}
	request.Header.Set("Authorization", "Bearer "+p.Key)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.HTTP.Do(request)
	if err != nil {
		// The transport's error names this deployment's proxy address,
		// which is not the caller's to know; the cause is one of two.
		if ctx.Err() != nil {
			return Verdict{Status: "UNREACHABLE", Message: "The model did not answer in time."}
		}
		return Verdict{Status: "UNREACHABLE", Message: "The model service cannot be reached right now."}
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxVerdictBody))
	if response.StatusCode == http.StatusOK {
		return Verdict{Status: "OK", OK: true, Message: "Model test succeeded."}
	}
	said := saidBy(raw, probe.Key)
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return Verdict{Status: "AUTH_FAILED", Message: orElse(said, "Authentication failed.")}
	case http.StatusNotFound:
		return Verdict{Status: "NOT_FOUND", Message: orElse(said, "The model was not found.")}
	}
	return Verdict{Status: "UNREACHABLE", Message: orElse(said, "The model test failed.")}
}

// saidBy is the sentence in the proxy's error body, shortened, with the
// connection's key (and any other secret given) struck out wherever a
// provider echoed it back.
func saidBy(raw []byte, key string, secrets ...string) string {
	var reply struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &reply) != nil {
		return ""
	}
	message := strings.Join(strings.Fields(reply.Error.Message), " ")
	if key != "" {
		message = strings.ReplaceAll(message, key, "[key]")
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[header]")
		}
	}
	if utf8.RuneCountInString(message) > maxMessage {
		message = string([]rune(message)[:maxMessage]) + "…"
	}
	return message
}

func orElse(said, otherwise string) string {
	if said == "" {
		return otherwise
	}
	return said
}
