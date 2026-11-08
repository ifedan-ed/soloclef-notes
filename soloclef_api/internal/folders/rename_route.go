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

// RenameRoute serves the rename.
type RenameRoute struct {
	Pool    *pgxpool.Pool
	Folders Store
	Access  Access
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	Now     func() time.Time
}

func (r RenameRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "rename-folder",
		Method:      http.MethodPut,
		Path:        RenamePath,
		Summary:     "Rename a folder",
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/folders/{folder_id}, byte for byte, and the same row.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		FolderID string `path:"folder_id" doc:"The folder's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r RenameRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder could not be renamed"}

	requested, validation, decodeErr := ReadRenameRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		fail.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	gate := folderGate{Folders: r.Folders, Access: r.Access, Errors: r.Errors}
	_, folder, open := gate.open(ctx, response, requestID, fail, validation,
		DocumentsUpdate, UpdateDenied)
	if !open {
		return
	}

	// `folder.name = request.name` only produces an UPDATE when the value
	// actually changes: SQLAlchemy compares the new value with the loaded one
	// and flushes nothing when they are equal, so `updated_at` is left alone.
	// Checked against the running tier with a long name that no string
	// interning could have made the same object.
	if requested.Name != folder.Name {
		at := nowOr(r.Now)
		if _, err := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, Rename(ctx, tx, folder.ID, requested.Name, at)
			}); err != nil {
			fail.fail(ctx, response, requestID, "could not rename the folder", err)
			return
		}
		folder.Name = requested.Name
		folder.UpdatedAt = at
	}

	sendFolder(ctx, response, requestID, fail, *folder)
}
