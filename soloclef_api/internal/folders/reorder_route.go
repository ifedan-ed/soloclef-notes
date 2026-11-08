package folders

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ReorderRoute serves the reorder among siblings.
//
// It is the one route in the slice whose body reaches a *library* rather than a
// column: `before_position` and `after_position` are unconstrained strings and
// go straight to fractional_indexing, which raises for a key it did not
// produce. So a made-up key, a pair in the wrong order and the empty string are
// each a sanitised 500 on both tiers rather than a 422
// (internal/fractionalindex).
type ReorderRoute struct {
	Pool    *pgxpool.Pool
	Folders Store
	Access  Access
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	Now     func() time.Time
}

func (r ReorderRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "reorder-folder",
		Method:      http.MethodPut,
		Path:        ReorderPath,
		Summary:     "Reorder a folder among its siblings",
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/folders/{folder_id}/reorder, byte for byte, and the same row.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		FolderID string `path:"folder_id" doc:"The folder's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ReorderRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder could not be reordered"}

	requested, validation, decodeErr := ReadReorderRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		fail.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	gate := folderGate{Folders: r.Folders, Access: r.Access, Errors: r.Errors}
	_, folder, open := gate.open(ctx, response, requestID, fail, validation,
		DocumentsUpdate, ReorderDenied)
	if !open {
		return
	}

	place := positions{Folders: r.Folders}
	position, err := place.next(ctx.Context(), folder.WorkspaceID, folder.ParentID,
		requested.Before, requested.After)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not place the folder among its siblings", err)
		return
	}

	// A key that happens to be the one the folder already holds writes
	// nothing: `before_position: "a0", after_position: "a1"` against a folder
	// at "a0V" is a 200 with `updated_at` untouched on both tiers, which only
	// a row diff sees.
	if position != folder.Position {
		at := nowOr(r.Now)
		if _, err := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, Reposition(ctx, tx, folder.ID, position, at)
			}); err != nil {
			fail.fail(ctx, response, requestID, "could not reorder the folder", err)
			return
		}
		folder.Position = position
		folder.UpdatedAt = at
	}

	sendFolder(ctx, response, requestID, fail, *folder)
}
