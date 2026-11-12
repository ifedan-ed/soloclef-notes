package folders

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/fractionalindex"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ReorderRoute serves the reorder among siblings: the folder is given a
// position between the two the client names.
//
// The two positions come from the client, which read them off its
// neighbours, so they can be anything. A pair no position lies between (a
// made-up key, the two in the wrong order, an empty string) is the
// client's mistake and is answered 400, not 500.
type ReorderRoute struct {
	Folders Rows
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
		Description: "Places a folder of the caller's between `before_position` and `after_position`; " +
			"with neither, after its last sibling.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *folderPath) (*huma.StreamResponse, error) {
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
		fail.fail(ctx, response, requestID, "a body reached the route that should not have", decodeErr)
		return
	}
	caller, folder, open := opening{Folders: r.Folders, Errors: r.Errors}.open(ctx, response, requestID, fail, validation)
	if !open {
		return
	}

	var position string
	if requested.Before != nil || requested.After != nil {
		// Naming either neighbour decides the position without reading the
		// siblings at all.
		between, err := fractionalindex.KeyBetween(requested.Before, requested.After)
		if err != nil {
			r.Errors.Write(response, http.StatusBadRequest, InvalidPosition, requestID)
			return
		}
		position = between
	} else {
		last, err := positions{Folders: r.Folders, UserID: caller.UserID}.last(ctx.Context(), folder.ParentID)
		if err != nil {
			fail.fail(ctx, response, requestID, "could not place the folder among its siblings", err)
			return
		}
		position = last
	}

	// A position the folder already holds writes nothing.
	if position != folder.Position {
		at := nowOr(r.Now)
		if err := r.Folders.Reposition(ctx.Context(), caller.UserID, folder.ID, position, at); err != nil {
			fail.fail(ctx, response, requestID, "could not reorder the folder", err)
			return
		}
		folder.Position = position
		folder.UpdatedAt = at
	}
	sendFolder(ctx, response, requestID, fail, *folder)
}
