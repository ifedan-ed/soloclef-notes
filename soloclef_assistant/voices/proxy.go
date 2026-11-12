package voices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Proxy is the model proxy (LiteLLM), which is the only thing this package
// ever calls. It asks it two things: which voices a model speaks, and for a
// sentence spoken in one of them.
type Proxy struct {
	// Base is the proxy's OpenAI-style base address (Settings.APIBase).
	Base string
	// Key is sent as a bearer token when it is not empty.
	Key string
	// Client is the HTTP client. Deadlines come from each call's context.
	Client *http.Client
}

// NewProxy is the proxy the settings name.
func NewProxy(settings Settings, client *http.Client) Proxy {
	return Proxy{Base: strings.TrimRight(strings.TrimSpace(settings.APIBase), "/"), Key: settings.APIKey, Client: client}
}

// The most of an answer that is read. The model list of a large proxy is a
// few hundred kilobytes, and a sentence of speech is tens.
const (
	maxModelInfoBytes = 16 << 20
	maxSpeechBytes    = 8 << 20
)

func (p Proxy) do(ctx context.Context, method, path string, body []byte, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, p.Base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if p.Key != "" {
		request.Header.Set("Authorization", "Bearer "+p.Key)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(answer)) > limit {
		return nil, fmt.Errorf("%s %s answered more than %d bytes", method, path, limit)
	}
	if response.StatusCode != http.StatusOK {
		// The start of the body is the proxy's own explanation. It goes to
		// the log through the error, never to a caller.
		return nil, fmt.Errorf("%s %s answered %d: %s", method, path, response.StatusCode, clip(answer, 300))
	}
	return answer, nil
}

func clip(text []byte, length int) string {
	if len(text) > length {
		text = text[:length]
	}
	return strings.TrimSpace(string(text))
}

// ModelVoices is the names of the voices the proxy says a model speaks: the
// `model_info.voices` of its entry in `GET /model/info`.
//
// A model name can stand for several deployments, which the proxy balances
// between, and each has an entry of its own. A voice is offered only if
// every one of them lists it, because a request for it may be sent to any.
// For the same reason a deployment that lists no voices at all is a wrong
// setting rather than something to work around: nothing is known about what
// it would do with a request.
func (p Proxy) ModelVoices(ctx context.Context, model string) ([]string, error) {
	answer, err := p.do(ctx, http.MethodGet, "/model/info", nil, maxModelInfoBytes)
	if err != nil {
		return nil, &UnreachableError{Cause: err}
	}
	var listing struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				Voices []string `json:"voices"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(answer, &listing); err != nil {
		return nil, &UnreachableError{Cause: fmt.Errorf("GET /model/info answered something that is not a model list: %w", err)}
	}

	var common []string
	deployments := 0
	for _, entry := range listing.Data {
		if entry.ModelName != model {
			continue
		}
		deployments++
		if len(entry.ModelInfo.Voices) == 0 {
			return nil, misconfigured(ServiceVariable,
				"names the model %q, and the proxy lists no voices for it (or for one of its deployments). "+
					"Add them to the model's entry in the proxy's configuration, under model_info.voices", model)
		}
		if deployments == 1 {
			common = entry.ModelInfo.Voices
			continue
		}
		common = inBoth(common, entry.ModelInfo.Voices)
	}
	if deployments == 0 {
		return nil, misconfigured(ServiceVariable,
			"names the model %q, which the proxy at %s does not serve", model, APIBaseVariable)
	}
	if len(common) == 0 {
		return nil, misconfigured(ServiceVariable,
			"names the model %q, whose deployments in the proxy have no voice in common", model)
	}
	return common, nil
}

// inBoth is the entries of first that second has too, in first's order.
func inBoth(first, second []string) []string {
	held := make(map[string]bool, len(second))
	for _, name := range second {
		held[name] = true
	}
	both := make([]string, 0, len(first))
	for _, name := range first {
		if held[name] {
			both = append(both, name)
		}
	}
	return both
}

// Speak asks for text spoken in a voice, as MP3, on the proxy's OpenAI-style
// speech route. This is the call that costs money, and Samples is its only
// caller.
//
// `voice` is the voice's Native: a name, or Vertex's object. No retry is
// made: the caller is a person pressing a play button, who can press it
// again.
func (p Proxy) Speak(ctx context.Context, model string, voice any, text string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model": model, "voice": voice, "input": text, "response_format": "mp3",
	})
	if err != nil {
		return nil, err
	}
	audio, err := p.do(ctx, http.MethodPost, "/audio/speech", body, maxSpeechBytes)
	if err != nil {
		return nil, err
	}
	if len(audio) == 0 {
		return nil, errors.New("POST /audio/speech answered no audio")
	}
	return audio, nil
}
