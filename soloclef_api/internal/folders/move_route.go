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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// MoveRoute serves the reparent. It has the most refusals of the nine and
// their order is load-bearing: the target's existence, then its workspace,
// then the two circular-reference cases, then the depth.
type MoveRoute struct {
	Pool    *pgxpool.Pool
	Folders Store
	Access  Access
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
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/folders/{folder_id}/move, byte for byte, and the same row.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		FolderID string `path:"folder_id" doc:"The folder's id"`
	}) (*huma.StreamResponse, error) {
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
		fail.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	gate := folderGate{Folders: r.Folders, Access: r.Access, Errors: r.Errors}
	_, folder, open := gate.open(ctx, response, requestID, fail, validation,
		DocumentsUpdate, MoveDenied)
	if !open {
		return
	}

	newParentID := requested.NewParentID.Value
	if requested.NewParentID.OutOfRange {
		fail.fail(ctx, response, requestID, "the target parent id is outside the range a request can use", nil)
		return
	}
	if newParentID != nil {
		target, err := r.Folders.Folder(ctx.Context(), *newParentID)
		if err != nil {
			fail.fail(ctx, response, requestID, "could not read the target parent folder", err)
			return
		}
		if target == nil {
			r.Errors.Write(response, http.StatusNotFound, TargetNotFound, requestID)
			return
		}
		if target.WorkspaceID != folder.WorkspaceID {
			r.Errors.Write(response, http.StatusBadRequest, TargetElsewhere, requestID)
			return
		}
	}

	refusal, err := r.checkNoCycle(ctx.Context(), folder.ID, newParentID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the target parent's ancestors", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	place := positions{Folders: r.Folders}
	subtreeDepth, err := r.Folders.SubtreeDepth(ctx.Context(), folder.ID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not measure the folder's own subtree", err)
		return
	}
	refusal, err = place.checkDepth(ctx.Context(), newParentID, subtreeDepth)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not measure the target parent's depth", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	position, err := place.next(ctx.Context(), folder.WorkspaceID, newParentID, nil, nil)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not place the folder among its new siblings", err)
		return
	}

	// Both columns are assigned, so the UPDATE runs unless both are already
	// what they would be set to — which the ORM decides the same way.
	if position != folder.Position || !sameParent(folder.ParentID, newParentID) {
		at := nowOr(r.Now)
		if _, err := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, Reparent(ctx, tx, folder.ID, newParentID, position, at)
			}); err != nil {
			fail.fail(ctx, response, requestID, "could not move the folder", err)
			return
		}
		folder.ParentID = newParentID
		folder.Position = position
		folder.UpdatedAt = at
	}

	sendFolder(ctx, response, requestID, fail, *folder)
}

// checkNoCycle is check_no_circular_reference: a folder cannot be moved into
// itself, nor into anything below it. The self case is an equality test there
// and so is answered before any query, which is why a folder moved into itself
// is refused even when the row has no ancestors to read.
func (r MoveRoute) checkNoCycle(ctx context.Context, folderID int64, newParentID *int64) (*workspaceaccess.Refusal, error) {
	if newParentID == nil {
		return nil, nil
	}
	if *newParentID == folderID {
		return &workspaceaccess.Refusal{
			Status:  http.StatusBadRequest,
			Message: MovedIntoItself,
		}, nil
	}
	chain, err := r.Folders.Ancestors(ctx, *newParentID)
	if err != nil {
		return nil, err
	}
	for _, link := range chain {
		if link.ID == folderID {
			return &workspaceaccess.Refusal{
				Status:  http.StatusBadRequest,
				Message: MovedIntoOwnSubree,
			}, nil
		}
	}
	return nil, nil
}

func sameParent(left, right *int64) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	}
	return *left == *right
}
