package modelconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// An OpenAI-compatible connection a person added is the one kind this tier
// calls itself (ownmodels, "The one exception"): its models are listed from
// its own GET {base}/models, and it is tested by asking it a question at
// its own POST {base}/chat/completions. Both go through the egress guard,
// so the address cannot reach inside this server's network unless the
// operator allowed that host.

// The codes of a listing that did not produce a list. The settings screen
// branches on them; each comes with a sentence it shows as it is.
const (
	// codeListUnsupported: the endpoint has no OpenAI model list. The
	// person types the model's name instead, which connecting tests.
	codeListUnsupported = "MODEL_LIST_UNSUPPORTED"
	codeAddressRefused  = "ADDRESS_REFUSED"
	codeKeyRefused      = "KEY_REFUSED"
	codeUnreachable     = "ENDPOINT_UNREACHABLE"
)

// endpointRefusal is a listing that failed, as the answer to give.
type endpointRefusal struct {
	status  int
	code    string
	message string
}

func (e *endpointRefusal) Error() string { return e.message }

// Endpoints lists and tests an OpenAI-compatible connection at its own
// address. EndpointClient is the one a server runs with.
type Endpoints interface {
	// Models is the endpoint's own list. A failure is an *endpointRefusal
	// saying what to tell the person.
	Models(ctx context.Context, probe Probe) ([]pickedModel, error)
	// Try asks one model a short question.
	Try(ctx context.Context, probe Probe) Verdict
}

// EndpointClient is Endpoints over the egress guard.
type EndpointClient struct {
	Egress ownmodels.Egress
	// Timeout bounds one listing or one test: a person is waiting on a
	// settings page.
	Timeout time.Duration
}

// Check reports what is missing. Called once at start-up.
func (c EndpointClient) Check() error {
	if c.Timeout <= 0 {
		return errors.New("modelconnections: the endpoint timeout must be positive")
	}
	return c.Egress.Check()
}

// maxModelList bounds the list read: a thousand models is about a hundred
// kilobytes.
const maxModelList = 4 << 20

// Models reads GET {base}/models in OpenAI's shape: {"data": [{"id": ...}]}.
func (c EndpointClient) Models(ctx context.Context, probe Probe) ([]pickedModel, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	response, err := c.call(ctx, probe, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, c.unreached(ctx, err, probe.BaseURL)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxModelList+1))
	switch status := response.StatusCode; {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, &endpointRefusal{http.StatusBadRequest, codeKeyRefused,
			fmt.Sprintf("The endpoint refused the key (GET /models answered %d). Check the key and the headers.", status)}
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
		// The endpoint speaks chat but keeps no list: many gateways and
		// single-model servers are like this.
		return nil, &endpointRefusal{http.StatusConflict, codeListUnsupported,
			fmt.Sprintf("This endpoint does not list its models (GET /models answered %d). Type the model name; connecting tests it.", status)}
	case status >= 300 && status < 400:
		return nil, &endpointRefusal{http.StatusBadGateway, codeUnreachable,
			fmt.Sprintf("The endpoint answered GET /models with a redirect (%d), which is not followed. Use the address it redirects to.", status)}
	case status != http.StatusOK:
		return nil, &endpointRefusal{http.StatusBadGateway, codeUnreachable,
			fmt.Sprintf("The endpoint answered GET /models with %d. Try again in a moment, or type the model name.", status)}
	}
	if len(raw) > maxModelList {
		return nil, &endpointRefusal{http.StatusBadGateway, codeUnreachable, "The endpoint's list of models is too large to read."}
	}
	var list struct {
		Data *[]struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &list) != nil || list.Data == nil {
		return nil, &endpointRefusal{http.StatusConflict, codeListUnsupported,
			"This endpoint's answer to GET /models is not an OpenAI list of models. Type the model name; connecting tests it."}
	}
	models := make([]pickedModel, 0, len(*list.Data))
	seen := map[string]bool{}
	for _, entry := range *list.Data {
		id := strings.TrimSpace(entry.ID)
		// A blank, an over-long or a repeated id is not a model the column
		// can hold once.
		if id == "" || utf8.RuneCountInString(id) > maxModelIDChars || seen[id] {
			continue
		}
		seen[id] = true
		facts := map[string]any{"source": "endpoint"}
		if entry.OwnedBy != "" && utf8.RuneCountInString(entry.OwnedBy) <= maxModelIDChars {
			facts["owned_by"] = entry.OwnedBy
		}
		catalog, _ := json.Marshal(facts)
		// The list says nothing of what a model can do: every capability
		// stays unknown, which the menus read as "not said otherwise".
		models = append(models, pickedModel{ModelID: id, Source: "DISCOVERED", Catalog: catalog})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ModelID < models[j].ModelID })
	return models, nil
}

// Try asks the model one short question and reads how it went, in the
// verdict words the settings screen already knows.
func (c EndpointClient) Try(ctx context.Context, probe Probe) Verdict {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"model":    probe.ModelID,
		"messages": []map[string]string{{"role": "user", "content": "Hello"}},
		"stream":   false,
	})
	response, err := c.call(ctx, probe, http.MethodPost, "/chat/completions", body)
	if err != nil {
		refused := c.unreached(ctx, err, probe.BaseURL)
		return Verdict{Status: "UNREACHABLE", Message: refused.(*endpointRefusal).message}
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxVerdictBody))
	if response.StatusCode == http.StatusOK {
		return Verdict{Status: "OK", OK: true, Message: "Model test succeeded."}
	}
	values := make([]string, 0, len(probe.Headers))
	for _, value := range probe.Headers {
		values = append(values, value)
	}
	said := saidBy(raw, probe.Key, values...)
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return Verdict{Status: "AUTH_FAILED", Message: orElse(said, "The endpoint refused the key.")}
	case http.StatusNotFound:
		return Verdict{Status: "NOT_FOUND", Message: orElse(said, "The endpoint has no model by that name.")}
	}
	return Verdict{Status: "UNREACHABLE", Message: orElse(said, fmt.Sprintf("The endpoint answered the test with %d.", response.StatusCode))}
}

// call makes one request to the endpoint through the guard.
func (c EndpointClient) call(ctx context.Context, probe Probe, method, path string, body []byte) (*http.Response, error) {
	base, err := ownmodels.CheckAddress(probe.BaseURL)
	if err != nil {
		return nil, err
	}
	client, err := c.Egress.Client(ctx, base)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, &ownmodels.Refusal{Reason: "The base URL is not a web address"}
	}
	// The connection's headers first, so that nothing below is replaced by
	// one (they were checked not to name these when they were stored).
	for name, value := range probe.Headers {
		request.Header.Set(name, value)
	}
	if probe.Key != "" {
		request.Header.Set("Authorization", "Bearer "+probe.Key)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return client.Do(request)
}

// unreached is the refusal for a request that got no answer: the guard's
// no, the time running out, or a network that failed. The transport's own
// error is not repeated: it names addresses this server resolved.
func (c EndpointClient) unreached(ctx context.Context, err error, base string) error {
	switch {
	case ownmodels.IsRefusal(err):
		return &endpointRefusal{http.StatusBadRequest, codeAddressRefused, ownmodels.RefusalSentence(err, base)}
	case ctx.Err() != nil:
		return &endpointRefusal{http.StatusBadGateway, codeUnreachable, "The endpoint did not answer in time."}
	}
	return &endpointRefusal{http.StatusBadGateway, codeUnreachable, "The endpoint could not be reached. Check the base URL."}
}
