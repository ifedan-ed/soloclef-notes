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

// MoveRoute serves the move under another parent. Its refusals come in one
// order: the target must be the caller's folder, then it must not be the
// folder itself or anything below it, then the result must not be too deep.
type MoveRoute struct {
	Folders Rows
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	Now     func() time.Time
}

func (r MoveRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "move-folder",
		Method:      http.MethodPut,
		Path:        MovePath,
		Summary:     "Move a folder to a new parent",
		Description: "Moves a folder of the caller's under another of their folders, " +
			"or to the top level when `new_parent_id` is null. It goes after its new last sibling.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *folderPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r MoveRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder could not be moved"}

	requested, validation, decodeErr := ReadMoveRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		fail.fail(ctx, response, requestID, "a body reached the route that should not have", decodeErr)
		return
	}
	caller, folder, open := opening{Folders: r.Folders, Errors: r.Errors}.open(ctx, response, requestID, fail, validation)
	if !open {
		return
	}

	newParentID := requested.NewParentID.Value
	if requested.NewParentID.OutOfRange {
		r.Errors.Write(response, http.StatusNotFound, TargetNotFound, requestID)
		return
	}
	if newParentID != nil {
		// The self case first: it needs no read, and "into itself" says
		// more than what the chain check would.
		if *newParentID == folder.ID {
			r.Errors.Write(response, http.StatusBadRequest, MovedIntoItself, requestID)
			return
		}
		// The target's ancestor chain answers two questions with one read:
		// an empty chain means the target is not a folder of the caller's,
		// and a chain holding the folder being moved means the target is
		// inside it.
		chain, err := r.Folders.Ancestors(ctx.Context(), caller.UserID, *newParentID)
		if err != nil {
			fail.fail(ctx, response, requestID, "could not read the target parent's ancestors", err)
			return
		}
		if len(chain) == 0 {
			r.Errors.Write(response, http.StatusNotFound, TargetNotFound, requestID)
			return
		}
		for _, link := range chain {
			if link.ID == folder.ID {
				r.Errors.Write(response, http.StatusBadRequest, MovedIntoOwnSubree, requestID)
				return
			}
		}
	}

	// A move to where the folder already is changes nothing, and must not
	// send it to the end of its own siblings.
	if sameFolder(folder.ParentID, newParentID) {
		sendFolder(ctx, response, requestID, fail, *folder)
		return
	}

	place := positions{Folders: r.Folders, UserID: caller.UserID}
	subtreeDepth, err := r.Folders.SubtreeDepth(ctx.Context(), caller.UserID, folder.ID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not measure the folder's own subtree", err)
		return
	}
	tooDeep, err := place.checkDepth(ctx.Context(), newParentID, subtreeDepth)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not measure the target parent's depth", err)
		return
	}
	if tooDeep != nil {
		r.Errors.Write(response, tooDeep.Status, tooDeep.Message, requestID)
		return
	}
	position, err := place.last(ctx.Context(), newParentID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not place the folder among its new siblings", err)
		return
	}

	at := nowOr(r.Now)
	err = r.Folders.Reparent(ctx.Context(), caller.UserID, folder.ID, newParentID, position, at)
	switch {
	case errors.Is(err, ErrNameTaken):
		r.Errors.Write(response, http.StatusConflict, NameTakenAtTarget, requestID)
		return
	case errors.Is(err, ErrParentGone):
		r.Errors.Write(response, http.StatusNotFound, TargetNotFound, requestID)
		return
	case err != nil:
		fail.fail(ctx, response, requestID, "could not move the folder", err)
		return
	}
	folder.ParentID = newParentID
	folder.Position = position
	folder.UpdatedAt = at
	sendFolder(ctx, response, requestID, fail, *folder)
}

// sameFolder compares two optional folder ids: two absent ones are the same
// place (the top level, or no folder), and so are two equal ids.
func sameFolder(before, after *int64) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return *before == *after
}
