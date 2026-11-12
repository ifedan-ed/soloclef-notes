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

// Lister reads an account's connections and their models: the statements of
// queries.sql. Neither hands back a stored key.
type Lister interface {
	// ConnectionsOfAccount is the account's own connections, by id.
	ConnectionsOfAccount(ctx context.Context, userID string) ([]ListedConnection, error)
	// ModelsOfAccount is every model of those connections, by id.
	ModelsOfAccount(ctx context.Context, userID string) ([]ListedModel, error)
}

// ConnectionsRoute serves GET /api/v1/model-connections: the caller's own
// connections, each with its models.
//
// The answer never carries a connection's stored key, only whether one is
// saved. Read the package comment, "The key", before changing what this route
// renders.
type ConnectionsRoute struct {
	Connections Lister
	Errors      errorenvelope.Writer
	Logger      *slog.Logger
}

// Register adds the operation. It takes no parameter: whose connections are
// listed is the caller, and nothing in the request can say otherwise.
func (r ConnectionsRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-model-connections",
		Method:      http.MethodGet,
		Path:        ConnectionsPath,
		Summary:     "The caller's own model connections",
		Description: "The connections the account made with its own provider " +
			"keys, each with its models. A stored key is never answered, only " +
			"whether one is saved.",
		Tags: []string{"model connections"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ConnectionsRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"the model connections could not be served", reason, cause)
	}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read of somebody's model settings if the wiring is ever wrong.
		failed(noCaller, nil)
		return
	}

	rows, err := r.Connections.ConnectionsOfAccount(ctx.Context(), caller.UserID)
	if err != nil {
		failed("could not read the connections", err)
		return
	}
	// An account with no connection has no models to read.
	if len(rows) > 0 {
		models, modelsErr := r.Connections.ModelsOfAccount(ctx.Context(), caller.UserID)
		if modelsErr != nil {
			failed("could not read the connections' models", modelsErr)
			return
		}
		AttachModels(rows, models)
	}

	items, err := BuildConnections(rows)
	if err != nil {
		// A jsonb column the response model refuses: one such row fails the
		// whole list.
		failed("a row could not be rendered", err)
		return
	}
	body, err := pythonjson.Marshal(items)
	if err != nil {
		failed("could not render the model connections", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
