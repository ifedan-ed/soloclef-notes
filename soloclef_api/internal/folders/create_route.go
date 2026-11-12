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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CreateRoute serves the create: a folder of the caller's, at the top level
// or under one of the caller's own folders.
type CreateRoute struct {
	Folders Rows
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	// Now is the clock created_at and updated_at come from. Overridable for
	// tests.
	Now func() time.Time
}

func (r CreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-folder",
		Method:      http.MethodPost,
		Path:        CreatePath,
		Summary:     "Create a folder",
		Description: "Creates a folder of the caller's, after its last sibling. " +
			"The body is `name` and an optional `parent_id`.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r CreateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder could not be created"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		fail.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	requested, validation, decodeErr := ReadCreateRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		// The body middleware answers for such a body before any route
		// runs, so reaching here means the two disagreed.
		fail.fail(ctx, response, requestID, "a body reached the route that should not have", decodeErr)
		return
	}
	if !reportInvalid(r.Errors, response, requestID, nil, validation) {
		return
	}

	// The parent has to be the caller's own folder. One that is another
	// account's is not found, in the same words as one that does not exist.
	parentID := requested.ParentID.Value
	if requested.ParentID.OutOfRange {
		r.Errors.Write(response, http.StatusNotFound, ParentNotFound, requestID)
		return
	}
	if parentID != nil {
		parent, err := r.Folders.Folder(ctx.Context(), caller.UserID, *parentID)
		if err != nil {
			fail.fail(ctx, response, requestID, "could not read the parent folder", err)
			return
		}
		if parent == nil {
			r.Errors.Write(response, http.StatusNotFound, ParentNotFound, requestID)
			return
		}
	}

	place := positions{Folders: r.Folders, UserID: caller.UserID}
	tooDeep, err := place.checkDepth(ctx.Context(), parentID, 0)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not measure the parent folder's depth", err)
		return
	}
	if tooDeep != nil {
		r.Errors.Write(response, tooDeep.Status, tooDeep.Message, requestID)
		return
	}
	position, err := place.last(ctx.Context(), parentID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not place the folder among its siblings", err)
		return
	}

	// One reading for both columns: a folder that has never changed says so
	// by carrying the same instant twice.
	at := nowOr(r.Now)
	created, err := r.Folders.Insert(ctx.Context(), caller.UserID, Folder{
		Name: requested.Name, Position: position, ParentID: parentID, CreatedAt: at, UpdatedAt: at,
	})
	switch {
	case errors.Is(err, ErrNameTaken):
		r.Errors.Write(response, http.StatusConflict, NameTakenHere, requestID)
		return
	case errors.Is(err, ErrParentGone):
		r.Errors.Write(response, http.StatusNotFound, ParentNotFound, requestID)
		return
	case err != nil:
		fail.fail(ctx, response, requestID, "could not write the folder", err)
		return
	}
	sendFolder(ctx, response, requestID, fail, created)
}
