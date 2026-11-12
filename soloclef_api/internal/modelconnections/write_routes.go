package modelconnections

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// The paths of the writes and the tests.
const (
	ConnectionPath  = "/api/v1/model-connections/{connection_id}"
	TestPreviewPath = "/api/v1/model-connections/test-preview"
	TestUpdatePath  = "/api/v1/model-connections/{connection_id}/test-update"
	ModelTestPath   = "/api/v1/models/{model_id}/test"

	// A connection's models: model_routes.go.
	VerifyPath          = "/api/v1/model-connections/{connection_id}/verify"
	DiscoverPath        = "/api/v1/model-connections/{connection_id}/discover"
	DiscoverPreviewPath = "/api/v1/model-connections/discover-preview"
	ModelsPath          = "/api/v1/model-connections/{connection_id}/models"
	ModelPath           = "/api/v1/models/{model_id}"
)

// Writer is the statements behind the writes and the tests (Store).
type Writer interface {
	IsAdministrator(ctx context.Context, userID string) (bool, error)
	// Create and Update take the key and the headers sealed; headers is
	// the object ownmodels.SealedHeaders made, merged into `extra`.
	Create(ctx context.Context, userID string, fields change, sealedKey string, headers json.RawMessage, picked []pickedModel) (int64, error)
	Update(ctx context.Context, userID string, id int64, fields change, sealedKey string, headers json.RawMessage) (bool, error)
	Delete(ctx context.Context, userID string, id int64) (bool, error)
	One(ctx context.Context, userID string, id int64) (ListedConnection, bool, error)
	Credentials(ctx context.Context, userID string, id int64) (StoredCredentials, bool, error)
	ModelCredentials(ctx context.Context, userID string, modelID int64) (string, StoredCredentials, bool, error)

	// The statements of a connection's models (store_models.go).
	Provider(ctx context.Context, userID string, id int64) (provider string, found bool, err error)
	ModelProvider(ctx context.Context, userID string, modelID int64) (provider string, found bool, err error)
	ModelToVerify(ctx context.Context, userID string, id int64) (modelID string, found bool, err error)
	StoreDiscovered(ctx context.Context, userID string, id int64, listed []pickedModel) ([]ListedModel, error)
	SetModelsEnabled(ctx context.Context, userID string, id int64, modelIDs []int64, enabled bool) ([]ListedModel, error)
	UpdateModel(ctx context.Context, userID string, modelID int64, fields modelChange) (ListedModel, bool, error)
}

// WriteRoutes serves making, changing, removing and testing a connection:
//
//	POST   /api/v1/model-connections                       make one, with its models
//	PUT    /api/v1/model-connections/{id}                  change the fields named
//	DELETE /api/v1/model-connections/{id}                  remove it and its models
//	POST   /api/v1/model-connections/test-preview          try one before it is made
//	POST   /api/v1/model-connections/{id}/test-update      try a change before it is saved
//	POST   /api/v1/models/{id}/test                        try a stored model
//	POST   /api/v1/model-connections/{id}/verify           try the connection with one of its models
//	POST   /api/v1/model-connections/discover-preview      a provider's models, before a connection is made
//	POST   /api/v1/model-connections/{id}/discover         store the provider's models on a connection
//	PATCH  /api/v1/model-connections/{id}/models           switch several of its models on or off
//	PUT    /api/v1/models/{id}                             change a stored model
//
// # Who may
//
// Any account, in an interactive session, for its own connections to an
// OpenAI-compatible endpoint (ownmodels.Provider): "anyone can add any
// OpenAI-compatible API and use it". This tier calls such an endpoint
// itself, to list its models, to test it and to chat with it, and every
// one of those calls goes through the egress guard (ownmodels.Egress), so
// the address a person names cannot be a way into this server's network.
//
// A connection to any other provider is an administrator's to make, test
// and change, as before: its test and its use go through the model proxy,
// which is handed the address and calls it from inside the deployment's
// network, where this tier's guard cannot follow. Moving a connection from
// one kind to the other is held to both kinds' rule, and everything else
// about a connection (removing it, switching and editing its models)
// follows its provider's rule, so the two kinds stay apart whole.
//
// # Whose
//
// Every statement is `WHERE id AND user_id = caller`. A connection that is
// another account's is answered exactly as one that does not exist.
//
// # The key
//
// It is sealed before it is stored (keyseal.go), never answered, and
// opened only to be sent to the model proxy by a test. A change that
// leaves `api_key` out keeps the stored key; `"api_key": null` removes it.
type WriteRoutes struct {
	Connections Writer
	Keys        KeySeal
	Prober      Prober
	// Catalogue lists a provider's models, from the model proxy
	// (proxy_catalogue.go).
	Catalogue Catalogue
	// Endpoints lists and tests an OpenAI-compatible connection at its own
	// address, through the egress guard (endpoint.go).
	Endpoints Endpoints
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

const (
	administratorRequired = "Only an administrator can connect this provider. Anyone can add an OpenAI-compatible connection."
	connectionAbsent      = "Connection not found"
	modelAbsent           = "Model not found"
	keyUnreadable         = "The saved key can no longer be read. Enter the key again and save the connection."
)

// Check reports a dependency that was left out. Called once at start-up,
// so that a route does not find out in front of a person.
func (r WriteRoutes) Check() error {
	switch {
	case r.Connections == nil:
		return errors.New("modelconnections: the write routes have no store")
	case r.Prober == nil:
		return errors.New("modelconnections: the write routes have no prober")
	case r.Catalogue == nil:
		return errors.New("modelconnections: the write routes have no model catalogue")
	case r.Endpoints == nil:
		return errors.New("modelconnections: the write routes have no client for OpenAI-compatible endpoints")
	case !r.Keys.Ready():
		return errors.New("modelconnections: the write routes have no key seal")
	case r.Logger == nil:
		return errors.New("modelconnections: the write routes have no logger")
	}
	return nil
}

// Register adds the eleven operations.
func (r WriteRoutes) Register(api huma.API) {
	for _, operation := range []struct {
		id, method, path, summary string
		serve                     func(huma.Context)
	}{
		{"create-model-connection", http.MethodPost, ConnectionsPath, "Make a model connection", r.create},
		{"update-model-connection", http.MethodPut, ConnectionPath, "Change a model connection", r.update},
		{"delete-model-connection", http.MethodDelete, ConnectionPath, "Remove a model connection", r.remove},
		{"test-model-connection-preview", http.MethodPost, TestPreviewPath, "Try a connection before it is made", r.testPreview},
		{"test-model-connection-update", http.MethodPost, TestUpdatePath, "Try a change to a connection before it is saved", r.testUpdate},
		{"test-model", http.MethodPost, ModelTestPath, "Try a stored model", r.testModel},
		{"verify-model-connection", http.MethodPost, VerifyPath, "Try a connection with one of its models", r.verify},
		{"discover-models-preview", http.MethodPost, DiscoverPreviewPath, "A provider's models, before a connection is made", r.discoverPreview},
		{"discover-models", http.MethodPost, DiscoverPath, "Store the provider's models on a connection", r.discover},
		{"switch-models", http.MethodPatch, ModelsPath, "Switch several of a connection's models on or off", r.switchModels},
		{"update-model", http.MethodPut, ModelPath, "Change a stored model", r.updateModel},
	} {
		serve := operation.serve
		huma.Register(api, huma.Operation{
			OperationID: operation.id, Method: operation.method, Path: operation.path,
			Summary: operation.summary, Tags: []string{"model connections"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: serve}, nil
		})
	}
}

// writeRequest is what each of the six starts with.
type writeRequest struct {
	ctx       huma.Context
	routes    WriteRoutes
	response  http.ResponseWriter
	requestID string
	userID    string
	// administrator is whether the caller administers the deployment:
	// what a connection to a provider other than an OpenAI-compatible
	// endpoint needs (mayConnect).
	administrator bool
	// what names the route in a log line.
	what string
}

// begin resolves the caller and applies the gate: a session, and the
// caller's standing read for mayConnect. ok is false when the request has
// been answered.
func (r WriteRoutes) begin(ctx huma.Context, what string) (writeRequest, bool) {
	request := writeRequest{ctx: ctx, routes: r, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), what: what}
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than a write for
		// nobody if the wiring is ever wrong.
		request.failed(noCaller, nil)
		return request, false
	}
	if !caller.IsSession() {
		r.Errors.Write(request.response, http.StatusForbidden, sessionRequired, request.requestID)
		return request, false
	}
	administrator, err := r.Connections.IsAdministrator(ctx.Context(), caller.UserID)
	if err != nil {
		request.failed("could not read the caller's standing", err)
		return request, false
	}
	request.userID, request.administrator = caller.UserID, administrator
	return request, true
}

// mayConnect applies "Who may" to a provider a route is about to name an
// address for, or to call: an OpenAI-compatible endpoint is anyone's, every
// other provider an administrator's. false is answered with the 403.
func (w writeRequest) mayConnect(providers ...string) bool {
	for _, provider := range providers {
		if provider != ownmodels.Provider && !w.administrator {
			w.refuse(http.StatusForbidden, administratorRequired)
			return false
		}
	}
	return true
}

// storedProvider is the provider of the caller's own connection, or the
// 404 answered.
func (w writeRequest) storedProvider(id int64) (string, bool) {
	provider, found, err := w.routes.Connections.Provider(w.ctx.Context(), w.userID, id)
	if err != nil {
		w.failed("could not read the connection", err)
		return "", false
	}
	if !found {
		w.refuse(http.StatusNotFound, connectionAbsent)
		return "", false
	}
	return provider, true
}

// sealedHeaders is the object to merge into `extra` for the headers the
// body named: nil when it named none.
func (w writeRequest) sealedHeaders(fields change) (json.RawMessage, bool) {
	if !fields.setHeaders {
		return nil, true
	}
	sealed, err := ownmodels.SealedHeaders(w.routes.Keys, w.userID, fields.headers)
	if err != nil {
		w.failed("could not seal the headers", err)
		return nil, false
	}
	return sealed, true
}

func (w writeRequest) failed(reason string, cause error) {
	writeFailure(w.ctx, w.routes.Errors, w.routes.Logger, w.response, w.requestID, w.what, reason, cause)
}

func (w writeRequest) refuse(status int, message string) {
	w.routes.Errors.Write(w.response, status, message, w.requestID)
}

// id reads a path id. An id no row can have is answered "not found" by
// the statement that looks for it, so only an id that is not a number is
// refused here.
func (w writeRequest) id(name string) (int64, bool) {
	id, failure, rangeErr := requestnumber.Parse(w.ctx.Param(name), "path", name)
	if failure != nil {
		w.routes.Errors.WriteValidation(w.response, w.requestID, []errorenvelope.Failure{*failure})
		return 0, false
	}
	if rangeErr != nil {
		return 0, true
	}
	return id, true
}

// body reads the request's JSON object.
func (w writeRequest) body() (body, bool) {
	fields, failures := readBody(requestbody.Buffered(w.ctx.Context()))
	if failures != nil {
		w.routes.Errors.WriteValidation(w.response, w.requestID, failures)
		return nil, false
	}
	return fields, true
}

// sealed is the text to store for a key the body named: "" for none.
func (w writeRequest) sealed(fields change) (string, bool) {
	if !fields.setKey || fields.key == "" {
		return "", true
	}
	sealed, err := w.routes.Keys.Seal(w.userID, fields.key)
	if err != nil {
		w.failed("could not seal the key", err)
		return "", false
	}
	return sealed, true
}

// answer writes the connection as the list would.
func (w writeRequest) answer(id int64) {
	connection, found, err := w.routes.Connections.One(w.ctx.Context(), w.userID, id)
	if err != nil {
		w.failed("could not read the connection back", err)
		return
	}
	if !found {
		// Removed between the write and the read, by the same account.
		w.refuse(http.StatusNotFound, connectionAbsent)
		return
	}
	item, err := buildConnection(connection)
	if err != nil {
		w.failed("the connection could not be rendered", err)
		return
	}
	rendered, err := pythonjson.Marshal(item)
	if err != nil {
		w.failed("could not render the connection", err)
		return
	}
	pythonjson.Send(w.response, http.StatusOK, rendered)
}

func (r WriteRoutes) create(ctx huma.Context) {
	request, ok := r.begin(ctx, "a model connection could not be made")
	if !ok {
		return
	}
	sent, ok := request.body()
	if !ok {
		return
	}
	fields, failures := readChange(sent, true)
	picked, pickedFailures := readPicked(sent)
	if failures = append(failures, pickedFailures...); len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	if !request.mayConnect(fields.provider) {
		return
	}
	if failures := providerRules(fields.provider, fields, true, false); len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	sealed, ok := request.sealed(fields)
	if !ok {
		return
	}
	headers, ok := request.sealedHeaders(fields)
	if !ok {
		return
	}
	id, err := r.Connections.Create(ctx.Context(), request.userID, fields, sealed, headers, picked)
	if err != nil {
		request.failed("could not store the connection", err)
		return
	}
	request.answer(id)
}

func (r WriteRoutes) update(ctx huma.Context) {
	request, ok := r.begin(ctx, "a model connection could not be changed")
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
	if len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	stored, ok := request.storedProvider(id)
	if !ok {
		return
	}
	provider := stored
	if fields.setProvider {
		provider = fields.provider
	}
	// Both kinds' rule: a connection may be neither taken from a provider
	// nor moved to one the caller may not connect.
	if !request.mayConnect(stored, provider) {
		return
	}
	if failures := providerRules(provider, fields, false, provider != stored); len(failures) > 0 {
		r.Errors.WriteValidation(request.response, request.requestID, failures)
		return
	}
	sealed, ok := request.sealed(fields)
	if !ok {
		return
	}
	headers, ok := request.sealedHeaders(fields)
	if !ok {
		return
	}
	if provider != ownmodels.Provider && !fields.setHeaders {
		// A connection that leaves the OpenAI-compatible kind leaves its
		// headers behind: no other provider is sent them.
		headers, fields.setHeaders = json.RawMessage(`{}`), true
	}
	found, err := r.Connections.Update(ctx.Context(), request.userID, id, fields, sealed, headers)
	if err != nil {
		request.failed("could not change the connection", err)
		return
	}
	if !found {
		request.refuse(http.StatusNotFound, connectionAbsent)
		return
	}
	request.answer(id)
}

func (r WriteRoutes) remove(ctx huma.Context) {
	request, ok := r.begin(ctx, "a model connection could not be removed")
	if !ok {
		return
	}
	id, ok := request.id("connection_id")
	if !ok {
		return
	}
	provider, ok := request.storedProvider(id)
	if !ok || !request.mayConnect(provider) {
		return
	}
	found, err := r.Connections.Delete(ctx.Context(), request.userID, id)
	if err != nil {
		request.failed("could not remove the connection", err)
		return
	}
	if !found {
		request.refuse(http.StatusNotFound, connectionAbsent)
		return
	}
	pythonjson.Send(request.response, http.StatusOK, []byte(`{"status":"deleted"}`))
}

// WriteRouteList is the eleven write, test and model routes as "METHOD path", for
// the lists the server keeps of what it answers.
func WriteRouteList() []string {
	return []string{
		http.MethodPost + " " + ConnectionsPath,
		http.MethodPut + " " + ConnectionPath,
		http.MethodDelete + " " + ConnectionPath,
		http.MethodPost + " " + TestPreviewPath,
		http.MethodPost + " " + TestUpdatePath,
		http.MethodPost + " " + ModelTestPath,
		http.MethodPost + " " + VerifyPath,
		http.MethodPost + " " + DiscoverPreviewPath,
		http.MethodPost + " " + DiscoverPath,
		http.MethodPatch + " " + ModelsPath,
		http.MethodPut + " " + ModelPath,
	}
}

// BodyRoutes is the ones of them that read a body: all but the delete, the
// test of a stored model, the verify and the refresh, which name
// everything in the path.
func BodyRoutes() []string {
	return []string{
		http.MethodPost + " " + ConnectionsPath,
		http.MethodPut + " " + ConnectionPath,
		http.MethodPost + " " + TestPreviewPath,
		http.MethodPost + " " + TestUpdatePath,
		http.MethodPost + " " + DiscoverPreviewPath,
		http.MethodPatch + " " + ModelsPath,
		http.MethodPut + " " + ModelPath,
	}
}
