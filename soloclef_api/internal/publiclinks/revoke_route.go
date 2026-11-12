package publiclinks

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Revoker takes an account's link down.
type Revoker interface {
	// Revoke reports false for a link the account does not have.
	Revoke(ctx context.Context, userID string, linkID int64) (bool, error)
}

// RevokeRoute serves DELETE /api/v1/snapshots/{snapshot_id}: the link stops
// working at once, because the public page reads the row this deletes.
//
// It owes nothing outside its own row: no queue message, no event. The
// sharing panel re-reads the list after it.
type RevokeRoute struct {
	Links  Revoker
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register adds the operation. The id is declared as a string so that one
// that is not a whole number is refused with this tier's own 422 envelope
// (internal/requestnumber) rather than with Huma's.
func (r RevokeRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "revoke-public-link",
		Method:      http.MethodDelete,
		Path:        RevokePath,
		Summary:     "Take down one public share link",
		Tags:        []string{"public links"},
	}, func(ctx context.Context, _ *struct {
		SnapshotID string `path:"snapshot_id" doc:"The public link's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r RevokeRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		serverError(ctx, r.Errors, r.Logger, response, requestID,
			"the public link could not be revoked", reason, cause)
	}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed(noCaller, nil)
		return
	}
	linkID, failure, rangeErr := requestnumber.Parse(ctx.Param("snapshot_id"), "path", "snapshot_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	// A whole number too wide for the column names no link. It is answered
	// as that, without sending Postgres a value it would fail on.
	removed := false
	if rangeErr == nil {
		var err error
		if removed, err = r.Links.Revoke(ctx.Context(), caller.UserID, linkID); err != nil {
			failed("could not revoke the public link", err)
			return
		}
	}
	if !removed {
		// Never made, already revoked, or another account's: one answer, so
		// the route does not say which ids are in use.
		r.Errors.Write(response, http.StatusNotFound, linkNotFound, requestID)
		return
	}

	body, err := pythonjson.Marshal(Message())
	if err != nil {
		failed("the link is revoked and its answer could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
