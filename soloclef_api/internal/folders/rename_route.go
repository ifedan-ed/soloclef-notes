package folders

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// RenameRoute serves the rename.
type RenameRoute struct {
	Folders Rows
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
		Description: "Renames a folder of the caller's. The body is `name`.",
		Tags:        []string{"folders"},
	}, func(ctx context.Context, _ *folderPath) (*huma.StreamResponse, error) {
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
		fail.fail(ctx, response, requestID, "a body reached the route that should not have", decodeErr)
		return
	}
	caller, folder, open := opening{Folders: r.Folders, Errors: r.Errors}.open(ctx, response, requestID, fail, validation)
	if !open {
		return
	}

	// A rename to the name the folder already has writes nothing, so
	// `updated_at` keeps meaning "when this folder last changed".
	if requested.Name != folder.Name {
		at := nowOr(r.Now)
		err := r.Folders.Rename(ctx.Context(), caller.UserID, folder.ID, requested.Name, at)
		if errors.Is(err, ErrNameTaken) {
			r.Errors.Write(response, http.StatusConflict, NameTakenHere, requestID)
			return
		}
		if err != nil {
			fail.fail(ctx, response, requestID, "could not rename the folder", err)
			return
		}
		folder.Name = requested.Name
		folder.UpdatedAt = at
	}
	sendFolder(ctx, response, requestID, fail, *folder)
}
