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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CreateRoute serves the create. It is the one route of the nine that gates on
// a workspace the *body* names rather than one a row names, which is why it
// checks the permission before it reads anything.
type CreateRoute struct {
	Pool    *pgxpool.Pool
	Folders Store
	Access  Access
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	// Now is the clock created_at and updated_at come from, as the Python
	// model's two defaults are evaluated in the application rather than by the
	// column. Overridable for tests.
	Now func() time.Time
}

func (r CreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-folder",
		Method:      http.MethodPost,
		Path:        CreatePath,
		Summary:     "Create a folder",
		Description: "The same response as the Python tier's POST /api/v1/folders, " +
			"byte for byte, and the same row.",
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
		// The middleware forwards such a body before the gate, so reaching
		// here means the two disagreed.
		fail.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}
	if len(validation) > 0 {
		r.Errors.WriteValidation(response, requestID, validation)
		return
	}
	if requested.WorkspaceOutOfRange {
		// Python holds the id and the permission query then fails on it, which
		// is after the body has validated.
		fail.fail(ctx, response, requestID, "the workspace id is outside the range a request can use", nil)
		return
	}

	refusal, err := checkGates(ctx.Context(), r.Access, caller,
		requested.WorkspaceID, DocumentsCreate, CreateDenied)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the caller's standing in the workspace", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	// The parent is read only after the permission, as there, so a caller who
	// may not create folders is told that rather than whether a parent exists.
	// A parent id too wide for the column is a number Python holds and the
	// lookup then fails on, so it is this route's 500 and not a root folder.
	if requested.ParentID.OutOfRange {
		fail.fail(ctx, response, requestID, "the parent id is outside the range a request can use", nil)
		return
	}
	parentID := requested.ParentID.Value
	if parentID != nil {
		parent, err := r.Folders.Folder(ctx.Context(), *parentID)
		if err != nil {
			fail.fail(ctx, response, requestID, "could not read the parent folder", err)
			return
		}
		if parent == nil {
			r.Errors.Write(response, http.StatusNotFound, ParentNotFound, requestID)
			return
		}
		if parent.WorkspaceID != requested.WorkspaceID {
			r.Errors.Write(response, http.StatusBadRequest, ParentElsewhere, requestID)
			return
		}
	}

	place := positions{Folders: r.Folders}
	refusal, err = place.checkDepth(ctx.Context(), parentID, 0)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not measure the parent folder's depth", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	position, err := place.next(ctx.Context(), requested.WorkspaceID, parentID, nil, nil)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not place the folder among its siblings", err)
		return
	}

	now := r.Now
	if now == nil {
		now = time.Now
	}
	// Two readings, in the order the Python model declares the two columns:
	// Folder declares updated_at and its TimestampMixin adds created_at after
	// it, so that tier's two defaults are evaluated in that order and differ
	// by a few microseconds. Both are minted either way.
	updatedAt := now().UTC()
	createdAt := now().UTC()
	userID := caller.UserID
	created, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Folder, error) {
			return Insert(ctx, tx, Folder{
				Name:        requested.Name,
				Position:    position,
				ParentID:    parentID,
				WorkspaceID: requested.WorkspaceID,
				CreatedByID: &userID,
				CreatedAt:   createdAt,
				UpdatedAt:   updatedAt,
			})
		})
	if err != nil {
		fail.fail(ctx, response, requestID, "could not write the folder", err)
		return
	}

	rendered, err := Build(created)
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the folder", err)
		return
	}
	body, err := marshal(rendered)
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the folder", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
