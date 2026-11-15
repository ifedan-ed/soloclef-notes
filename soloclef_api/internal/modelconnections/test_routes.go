package modelconnections

import (
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// The three tests. Each builds a Probe and asks the endpoint itself a short
// question, through the egress guard (endpoint.go); none stores anything.
// A test that fails is a 200 whose verdict says so: the request to this
// tier succeeded, and the settings screen shows the verdict's sentence
// beside the connection.

// modelID reads the `model_id` a test body names.
func modelID(sent body) (string, []errorenvelope.Failure) {
	var failures []errorenvelope.Failure
	id, _ := sent.text("model_id", maxModelIDChars, &failures)
	if id = strings.TrimSpace(id); id == "" && failures == nil {
		failures = append(failures, invalid("model_id", "model_id is required"))
	}
	return id, failures
}

// verdict tries the probe and answers how it went.
func (w writeRequest) verdict(probe Probe) {
	w.sendJSON(w.routes.Endpoints.Try(w.ctx.Context(), probe))
}

// opened is a stored connection's key. A key this deployment's secret can
// no longer open is said in words a person can act on.
func (w writeRequest) opened(stored StoredCredentials) (string, bool) {
	if stored.SealedKey == "" {
		return "", true
	}
	key, err := w.routes.Keys.Open(w.userID, stored.SealedKey)
	if errors.Is(err, ErrSealUnreadable) {
		w.refuse(http.StatusConflict, keyUnreadable)
		return "", false
	}
	if err != nil {
		w.failed("could not open the stored key", err)
		return "", false
	}
	return key, true
}

// openedHeaders is a stored connection's headers, opened, as opened is its
// key.
func (w writeRequest) openedHeaders(stored StoredCredentials) (map[string]string, bool) {
	headers, err := ownmodels.OpenHeaders(w.routes.Keys, w.userID, ownmodels.SealedHeadersOf(stored.Extra))
	if errors.Is(err, ErrSealUnreadable) {
		w.refuse(http.StatusConflict, keyUnreadable)
		return nil, false
	}
	if err != nil {
		w.failed("could not open the stored headers", err)
		return nil, false
	}
	return headers, true
}

// stored is a stored connection as a Probe of one of its models: the key
// and the headers opened.
func (w writeRequest) stored(stored StoredCredentials, model string) (Probe, bool) {
	probe := Probe{BaseURL: stored.BaseURL, ModelID: model}
	var ok bool
	if probe.Key, ok = w.opened(stored); !ok {
		return Probe{}, false
	}
	if probe.Headers, ok = w.openedHeaders(stored); !ok {
		return Probe{}, false
	}
	return probe, true
}

// testPreview tries a connection that is not stored: the body is the
// connection as it would be made, key included, plus the model to try.
func (r WriteRoutes) testPreview(ctx huma.Context) {
	request, ok := r.begin(ctx, "a draft model connection could not be tested")
	if !ok {
		return
	}
	sent, ok := request.body()
	if !ok {
		return
	}
	fields, failures := readChange(sent, true)
	model, modelFailures := modelID(sent)
	if failures = append(failures, modelFailures...); len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	if failures := providerRules(fields, true); len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	request.verdict(Probe{BaseURL: fields.baseURL, Key: fields.key, Headers: fields.headers, ModelID: model})
}

// testUpdate tries a stored connection as it would be after a change,
// saving nothing. It exists because the browser never holds the stored
// key: the body reads exactly as the PUT that would follow it, so a field
// that is present is the proposed value, null included, and a field that
// is absent is the stored one. Leaving `api_key` out tests with the saved
// key.
func (r WriteRoutes) testUpdate(ctx huma.Context) {
	request, ok := r.begin(ctx, "a change to a model connection could not be tested")
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
	fields, failures := readChange(sent, false)
	model, modelFailures := modelID(sent)
	if failures = append(failures, modelFailures...); len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
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
	// As the save that would follow it: a row of a provider that is gone
	// is refused, and the fields named are held to the rules.
	if !request.supported(stored.Provider) {
		return
	}
	probe := Probe{BaseURL: stored.BaseURL, ModelID: model}
	if failures := providerRules(fields, false); len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	if fields.setBaseURL {
		probe.BaseURL = fields.baseURL
	}
	if fields.setKey {
		probe.Key = fields.key
	} else if probe.Key, ok = request.opened(stored); !ok {
		return
	}
	if fields.setHeaders {
		probe.Headers = fields.headers
	} else if probe.Headers, ok = request.openedHeaders(stored); !ok {
		return
	}
	request.verdict(probe)
}

// testModel tries a stored model with its connection as stored.
func (r WriteRoutes) testModel(ctx huma.Context) {
	request, ok := r.begin(ctx, "a stored model could not be tested")
	if !ok {
		return
	}
	id, ok := request.id("model_id")
	if !ok {
		return
	}
	model, stored, found, err := r.Connections.ModelCredentials(ctx.Context(), request.userID, id)
	if err != nil {
		request.failed("could not read the model", err)
		return
	}
	if !found {
		request.refuse(http.StatusNotFound, modelAbsent)
		return
	}
	if !request.supported(stored.Provider) {
		return
	}
	probe, ok := request.stored(stored, model)
	if !ok {
		return
	}
	request.verdict(probe)
}
