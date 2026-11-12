package modelconnections

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// A connection's models: trying the connection with one of them, listing
// what its provider offers, and switching and editing the ones stored.
// They are WriteRoutes' because they share its gate (a session, and
// mayConnect) and its rule of ownership.
//
// The list of a provider's models is the model proxy's catalogue
// (proxy_catalogue.go), and a connection is tried by asking one of its
// models a question through the proxy, for every provider but one. An
// OpenAI-compatible connection is listed from its own GET {base}/models and
// tried at its own address, through the egress guard (endpoint.go).

const (
	// noCatalogueMessage is the refusal for a provider the catalogue does
	// not cover. It says what to do instead, because there is something.
	noCatalogueMessage = "The model service has no list of models for this provider: its models are the ones at the address you gave. Add each model by its name; connecting tests it."
	catalogueDown      = "The model service's list of models cannot be read right now. Please try again."
	// maxSwitchedModels is how many models one request may switch.
	maxSwitchedModels = 1000
)

// noModelVerdict answers a verify of a connection with no model. A key
// cannot be tried on its own through the proxy: every request it takes
// names a model. Saying so is the truth, where the Python route answered
// "verified" for any provider it had no cheap request for.
var noModelVerdict = Verdict{
	Status:  "NO_MODEL",
	Message: "This connection has no model to try it with. Add a model, then test the connection.",
}

// verify tries a stored connection with one of its own models: an enabled
// chat model if it has one (queries.sql, ModelToVerify).
func (r WriteRoutes) verify(ctx huma.Context) {
	request, ok := r.begin(ctx, "a model connection could not be verified")
	if !ok {
		return
	}
	id, ok := request.id("connection_id")
	if !ok {
		return
	}
	stored, found, err := r.Connections.Credentials(ctx.Context(), request.userID, id)
	if err != nil {
		request.failed("could not read the connection", err)
		return
	}
	if !found {
		request.refuse(http.StatusNotFound, connectionAbsent)
		return
	}
	model, found, err := r.Connections.ModelToVerify(ctx.Context(), request.userID, id)
	if err != nil {
		request.failed("could not choose a model to verify with", err)
		return
	}
	if !request.mayConnect(stored.Provider) {
		return
	}
	if !found {
		request.sendJSON(noModelVerdict)
		return
	}
	probe, ok := request.stored(stored, model)
	if !ok {
		return
	}
	request.verdict(probe)
}

// sendJSON writes a 200 with a value encoding/json renders.
func (w writeRequest) sendJSON(value any) {
	rendered, err := json.Marshal(value)
	if err != nil {
		w.failed("could not render the answer", err)
		return
	}
	w.response.Header().Set("Content-Type", "application/json")
	w.response.WriteHeader(http.StatusOK)
	_, _ = w.response.Write(rendered)
}

// listed is the models of a connection as probe describes it: the
// endpoint's own list for an OpenAI-compatible connection, the proxy's
// catalogue for every other provider. The refusal is written when there is
// no list.
func (w writeRequest) listed(probe Probe) ([]pickedModel, bool) {
	if probe.Provider != ownmodels.Provider {
		return w.catalogued(probe.Provider)
	}
	models, err := w.routes.Endpoints.Models(w.ctx.Context(), probe)
	var refused *endpointRefusal
	switch {
	case errors.As(err, &refused):
		// The sentence is about the address and the answer the person's
		// endpoint gave; it carries neither the key nor a header.
		w.routes.Errors.WriteCoded(w.response, refused.status, refused.code, refused.message, w.requestID)
		return nil, false
	case err != nil:
		w.failed("could not list the endpoint's models", err)
		return nil, false
	}
	return models, true
}

// catalogued is the provider's models from the proxy's catalogue, or the
// refusal written.
func (w writeRequest) catalogued(provider string) ([]pickedModel, bool) {
	models, err := w.routes.Catalogue.Models(w.ctx.Context(), provider)
	switch {
	case errors.Is(err, ErrNoCatalogue):
		w.refuse(http.StatusConflict, noCatalogueMessage)
		return nil, false
	case err != nil:
		// The cause names this deployment's proxy; the person is told
		// only that the list could not be read.
		w.routes.Logger.ErrorContext(w.ctx.Context(), w.what,
			"request_id", w.requestID, "reason", "the model proxy's catalogue could not be read", "error", err.Error())
		w.refuse(http.StatusBadGateway, catalogueDown)
		return nil, false
	}
	return models, true
}

// previewedModel is a catalogue model as the connect dialog lists one,
// before any connection exists: the apps call its catalogue entry
// `metadata` there and send it back under that name (readPicked).
type previewedModel struct {
	ModelID                 string          `json:"model_id"`
	DisplayName             *string         `json:"display_name"`
	Source                  string          `json:"source"`
	SupportsChat            *bool           `json:"supports_chat"`
	MaxInputTokens          *int32          `json:"max_input_tokens"`
	SupportsImageInput      *bool           `json:"supports_image_input"`
	SupportsTools           *bool           `json:"supports_tools"`
	SupportsImageGeneration *bool           `json:"supports_image_generation"`
	Enabled                 bool            `json:"enabled"`
	Metadata                json.RawMessage `json:"metadata"`
}

// discoverPreview lists a provider's models for a connection that is not
// made yet. For an OpenAI-compatible endpoint the body is the connection as
// it would be made (base_url, api_key, headers) and its own list is read.
// For every other provider only the provider is read: the catalogue is the
// same whatever the key and the address, so a key that was sent is neither
// parsed nor forwarded.
func (r WriteRoutes) discoverPreview(ctx huma.Context) {
	request, ok := r.begin(ctx, "a provider's models could not be listed")
	if !ok {
		return
	}
	sent, ok := request.body()
	if !ok {
		return
	}
	var failures []errorenvelope.Failure
	provider, named := sent.text("provider", maxProviderChars, &failures)
	if provider = strings.TrimSpace(provider); failures == nil && (!named || !knownProvider(provider)) {
		failures = append(failures, invalid("provider", "provider must be one of the providers this server lists"))
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	if !request.mayConnect(provider) {
		return
	}
	probe := Probe{Provider: provider}
	if provider == ownmodels.Provider {
		fields, failures := readChange(sent, true)
		if failures = append(failures, providerRules(provider, fields, true, false)...); len(failures) > 0 {
			r.Errors.WriteValidation(request.response, request.requestID, failures)
			return
		}
		probe.BaseURL, probe.Key, probe.Headers = fields.baseURL, fields.key, fields.headers
	}
	models, ok := request.listed(probe)
	if !ok {
		return
	}
	listed := make([]previewedModel, 0, len(models))
	for _, model := range models {
		listed = append(listed, previewedModel{
			ModelID: model.ModelID, Source: model.Source, SupportsChat: model.SupportsChat,
			MaxInputTokens: model.MaxInputTokens, SupportsImageInput: model.SupportsImageInput,
			SupportsTools: model.SupportsTools, SupportsImageGeneration: model.SupportsImageGeneration,
			Metadata: model.Catalog,
		})
	}
	request.sendJSON(listed)
}

// discover stores the provider's models on a stored connection and answers
// all of the connection's models. New ones arrive switched off.
func (r WriteRoutes) discover(ctx huma.Context) {
	request, ok := r.begin(ctx, "a connection's models could not be refreshed")
	if !ok {
		return
	}
	id, ok := request.id("connection_id")
	if !ok {
		return
	}
	stored, found, err := r.Connections.Credentials(ctx.Context(), request.userID, id)
	if err != nil {
		request.failed("could not read the connection", err)
		return
	}
	if !found {
		request.refuse(http.StatusNotFound, connectionAbsent)
		return
	}
	if !request.mayConnect(stored.Provider) {
		return
	}
	probe := Probe{Provider: stored.Provider}
	if stored.Provider == ownmodels.Provider {
		// The endpoint's own list needs its key and headers; no other
		// provider's does, and for those the key stays sealed.
		if probe, ok = request.stored(stored, ""); !ok {
			return
		}
	}
	listed, ok := request.listed(probe)
	if !ok {
		return
	}
	models, err := r.Connections.StoreDiscovered(ctx.Context(), request.userID, id, listed)
	if err != nil {
		request.failed("could not store the provider's models", err)
		return
	}
	request.models(models)
}

// models writes stored models as the connection list renders them.
func (w writeRequest) models(models []ListedModel) {
	items := make([]any, 0, len(models))
	for _, model := range models {
		item, err := buildModel(model)
		if err != nil {
			w.failed("a model could not be rendered", err)
			return
		}
		items = append(items, item)
	}
	rendered, err := pythonjson.Marshal(items)
	if err != nil {
		w.failed("could not render the models", err)
		return
	}
	pythonjson.Send(w.response, http.StatusOK, rendered)
}

// switchModels turns several models of one connection on or off and
// answers the ones it changed. An id that is not a model of this
// connection changes nothing and is not in the answer.
func (r WriteRoutes) switchModels(ctx huma.Context) {
	request, ok := r.begin(ctx, "a connection's models could not be switched")
	if !ok {
		return
	}
	id, ok := request.id("connection_id")
	if !ok {
		return
	}
	sent, ok := request.body()
	if !ok {
		return
	}
	var (
		modelIDs []int64
		enabled  bool
		failures []errorenvelope.Failure
	)
	if err := json.Unmarshal(sent["model_ids"], &modelIDs); err != nil || len(modelIDs) == 0 || len(modelIDs) > maxSwitchedModels {
		failures = append(failures, invalid("model_ids", "model_ids must be a list of 1 to 1000 model ids"))
	}
	if raw := sent["enabled"]; json.Unmarshal(raw, &enabled) != nil || string(raw) == "null" {
		failures = append(failures, invalid("enabled", "enabled must be true or false"))
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	// Asked first so that a connection that is not the caller's is "not
	// found" and not an empty list.
	if provider, ok := request.storedProvider(id); !ok || !request.mayConnect(provider) {
		return
	}
	models, err := r.Connections.SetModelsEnabled(ctx.Context(), request.userID, id, modelIDs, enabled)
	if err != nil {
		request.failed("could not switch the models", err)
		return
	}
	request.models(models)
}

// modelChange is the fields of a model a body named, read as a
// connection's are (change): present is the value, null included, and
// absent is not being changed.
type modelChange struct {
	displayName                *string
	setDisplayName             bool
	enabled                    bool
	setEnabled                 bool
	supportsChat               *bool
	setSupportsChat            bool
	maxInputTokens             *int64
	setMaxInputTokens          bool
	supportsImageInput         *bool
	setSupportsImageInput      bool
	supportsTools              *bool
	setSupportsTools           bool
	supportsImageGeneration    *bool
	setSupportsImageGeneration bool
	// capabilitiesOverride is a JSON object, compact.
	capabilitiesOverride    []byte
	setCapabilitiesOverride bool
}

// maxOverrideBytes bounds a model's capabilities_override, which holds a
// handful of true/false switches.
const maxOverrideBytes = 16 << 10

// readModelChange reads the model fields of a body.
func readModelChange(b body) (modelChange, []errorenvelope.Failure) {
	var (
		read     modelChange
		failures []errorenvelope.Failure
	)
	if name, named := b.text("display_name", maxModelIDChars, &failures); named {
		read.setDisplayName = true
		if name = strings.TrimSpace(name); name != "" {
			read.displayName = &name
		}
	}
	if raw, named := b["enabled"]; named {
		read.setEnabled = true
		if err := json.Unmarshal(raw, &read.enabled); err != nil || string(raw) == "null" {
			failures = append(failures, invalid("enabled", "enabled must be true or false"))
		}
	}
	// A capability may be true, false or null: null is "not known", which
	// is what a model nobody has described starts as.
	for _, capability := range []struct {
		field string
		value **bool
		set   *bool
	}{
		{"supports_chat", &read.supportsChat, &read.setSupportsChat},
		{"supports_image_input", &read.supportsImageInput, &read.setSupportsImageInput},
		{"supports_tools", &read.supportsTools, &read.setSupportsTools},
		{"supports_image_generation", &read.supportsImageGeneration, &read.setSupportsImageGeneration},
	} {
		raw, named := b[capability.field]
		if !named {
			continue
		}
		*capability.set = true
		if json.Unmarshal(raw, capability.value) != nil {
			failures = append(failures, invalid(capability.field, capability.field+" must be true, false or null"))
		}
	}
	if raw, named := b["max_input_tokens"]; named {
		read.setMaxInputTokens = true
		// Null clears the limit, which lets the next refresh set it again.
		if string(raw) != "null" {
			var limit int32
			if err := json.Unmarshal(raw, &limit); err != nil || limit <= 0 {
				failures = append(failures, invalid("max_input_tokens", "max_input_tokens must be a positive whole number or null"))
			} else {
				wide := int64(limit)
				read.maxInputTokens = &wide
			}
		}
	}
	if raw, named := b["capabilities_override"]; named {
		read.setCapabilitiesOverride = true
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil || len(raw) > maxOverrideBytes || !utf8.Valid(raw) {
			failures = append(failures, invalid("capabilities_override", "capabilities_override must be a small object"))
		} else {
			var compact bytes.Buffer
			_ = json.Compact(&compact, raw)
			read.capabilitiesOverride = compact.Bytes()
		}
	}
	return read, failures
}

// updateModel changes the fields named on a model of one of the caller's
// own connections.
func (r WriteRoutes) updateModel(ctx huma.Context) {
	request, ok := r.begin(ctx, "a stored model could not be changed")
	if !ok {
		return
	}
	id, ok := request.id("model_id")
	if !ok {
		return
	}
	sent, ok := request.body()
	if !ok {
		return
	}
	fields, failures := readModelChange(sent)
	if len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	provider, found, err := r.Connections.ModelProvider(ctx.Context(), request.userID, id)
	if err != nil {
		request.failed("could not read the model", err)
		return
	}
	if !found {
		request.refuse(http.StatusNotFound, modelAbsent)
		return
	}
	if !request.mayConnect(provider) {
		return
	}
	model, found, err := r.Connections.UpdateModel(ctx.Context(), request.userID, id, fields)
	if err != nil {
		request.failed("could not change the model", err)
		return
	}
	if !found {
		request.refuse(http.StatusNotFound, modelAbsent)
		return
	}
	item, err := buildModel(model)
	if err != nil {
		request.failed("the model could not be rendered", err)
		return
	}
	rendered, err := pythonjson.Marshal(item)
	if err != nil {
		request.failed("could not render the model", err)
		return
	}
	pythonjson.Send(request.response, http.StatusOK, rendered)
}
