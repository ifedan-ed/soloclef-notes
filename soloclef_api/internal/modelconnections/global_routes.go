package modelconnections

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The paths of the site's own model state.
const (
	GlobalConnectionsPath = "/api/v1/global-model-connections"
	GlobalStatusPath      = "/api/v1/global-llm-config-status"
)

// GlobalLister is the statements behind the two routes (Store).
type GlobalLister interface {
	GlobalConnections(ctx context.Context) ([]ListedConnection, error)
	GlobalConnectionExists(ctx context.Context) (bool, error)
}

// GlobalRoutes serves what the site itself offers every account:
//
//	GET /api/v1/global-model-connections   the operator's connections and their models
//	GET /api/v1/global-llm-config-status   {"exists": whether one is switched on}
//
// Both are for the apps' own screens, so a session only, as the provider
// list is.
//
// The Python tier read these from a YAML file on the server; here they
// are the rows with scope "global" and no owner. Two things are better.
// The answer carries a connection's provider, its switch and its models
// and nothing else: the Python answered the operator's address, which may
// be inside the deployment's network, to every signed-in account, and this
// does not select it. And `exists` is about what an account can use (an
// enabled connection), where a file that existed and listed nothing was
// answered true.
type GlobalRoutes struct {
	Connections GlobalLister
	Errors      errorenvelope.Writer
	Logger      *slog.Logger
}

// GlobalRouteList is the two routes as "METHOD path".
func GlobalRouteList() []string {
	return []string{
		http.MethodGet + " " + GlobalConnectionsPath,
		http.MethodGet + " " + GlobalStatusPath,
	}
}

// Register adds the two operations.
func (r GlobalRoutes) Register(api huma.API) {
	for _, operation := range []struct {
		id, path, summary string
		answer            func(context.Context) ([]byte, error)
	}{
		{"read-global-model-connections", GlobalConnectionsPath, "The site's own model connections", r.connections},
		{"read-global-llm-config-status", GlobalStatusPath, "Whether the site has a model connection of its own", r.status},
	} {
		answer := operation.answer
		huma.Register(api, huma.Operation{
			OperationID: operation.id, Method: http.MethodGet, Path: operation.path,
			Summary: operation.summary, Tags: []string{"model connections"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) { r.serve(ctx, answer) }}, nil
		})
	}
}

func (r GlobalRoutes) serve(ctx huma.Context, answer func(context.Context) ([]byte, error)) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID, "the site's model state could not be served", noCaller, nil)
		return
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return
	}
	body, err := answer(ctx.Context())
	if err != nil {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID, "the site's model state could not be served", "could not read or render it", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r GlobalRoutes) connections(ctx context.Context) ([]byte, error) {
	rows, err := r.Connections.GlobalConnections(ctx)
	if err != nil {
		return nil, err
	}
	items, err := BuildConnections(rows)
	if err != nil {
		return nil, err
	}
	return pythonjson.Marshal(items)
}

func (r GlobalRoutes) status(ctx context.Context) ([]byte, error) {
	exists, err := r.Connections.GlobalConnectionExists(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return []byte(`{"exists":true}`), nil
	}
	return []byte(`{"exists":false}`), nil
}
