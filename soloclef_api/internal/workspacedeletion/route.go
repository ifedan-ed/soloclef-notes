package workspacedeletion

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Access is the membership and workspace reader the gates need.
type Access interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*workspaceaccess.Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (workspaceaccess.Workspace, error)
}

// Route serves the delete.
type Route struct {
	// Pool is taken rather than a store because the route runs two
	// transactions: the marker, and the revert for an enqueue that failed.
	Pool   *pgxpool.Pool
	Access Access
	Queue  celeryqueue.Enqueuer
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-workspace",
		Method:      http.MethodDelete,
		Path:        Path,
		Summary:     "Delete a workspace",
		Description: "The same response as the Python tier's " +
			"DELETE /api/v1/workspaces/{workspace_id}, byte for byte, the same " +
			"name marked, and the same Celery message on the same queue.",
		Tags: []string{"workspaces"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// delete if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	workspaceID, failure, rangeErr := requestnumber.Parse(
		ctx.Param("workspace_id"), "path", "workspace_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	previousName, refusal, err := r.mark(ctx.Context(), caller, workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not mark the workspace for deletion", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	// After the commit, as there: the task reads the workspace it is about.
	if _, err := r.Queue.Enqueue(ctx.Context(), DeletionOf(workspaceID,
		celeryqueue.LogContextFor(ctx.URL().Path, requestID, caller.UserID))); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"could not queue a workspace's deletion",
			slog.String("request_id", requestID),
			slog.Int64("workspace_id", workspaceID),
			slog.String("error", err.Error()))
		// The Python route's own compensation: a workspace left named
		// "[DELETING] …" that nothing is deleting cannot be deleted again.
		if _, revertErr := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, New(tx).RenameWorkspace(ctx, RenameWorkspaceParams{
					ID: workspaceID, Name: previousName,
				})
			}); revertErr != nil {
			r.fail(ctx, response, requestID, "could not restore a workspace whose deletion was not queued", revertErr)
			return
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, QueueFailed, requestID)
		return
	}

	body, err := pythonjson.Marshal(Message())
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// mark is the gates, the refusals and the marker, and it returns the name the
// workspace had so an enqueue that fails can put it back.
//
// The permission is checked before the workspace is read, which is the Python
// route's order and the opposite of the document delete's: there the state of
// the row decides a refusal that applies to everyone, here nothing is read
// until the caller may delete.
func (r Route) mark(ctx context.Context, caller principal.Principal, workspaceID int64) (string, *workspaceaccess.Refusal, error) {
	membership, err := r.Access.Membership(ctx, caller.UserID, workspaceID)
	if err != nil {
		return "", nil, err
	}
	if refusal := workspaceaccess.CheckMembership(membership, SettingsDelete, DeleteDenied); refusal != nil {
		return "", refusal, nil
	}
	gate, err := r.Access.Workspace(ctx, workspaceID)
	if err != nil {
		return "", nil, err
	}
	if refusal := workspaceaccess.CheckAPIAccess(gate, caller.IsGated()); refusal != nil {
		return "", refusal, nil
	}

	type outcome struct {
		previousName string
		refusal      *workspaceaccess.Refusal
	}
	// The name is read, the two refusals decided and the marker written in one
	// transaction, because the marker is built *from* the name: a name read in
	// one transaction and prefixed in another could be prefixed onto a name
	// that had changed in between, and the column is 100 characters wide.
	// It does not serialise two deletes of the same workspace — READ COMMITTED
	// lets both read the unmarked name — and neither does the Python route,
	// which takes no lock either.
	result, err := database.Write(ctx, r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			queries := New(tx)
			name, err := queries.WorkspaceForDeletion(ctx, workspaceID)
			if errors.Is(err, pgx.ErrNoRows) {
				return outcome{refusal: &workspaceaccess.Refusal{
					Status: http.StatusNotFound, Message: NotFound,
				}}, nil
			}
			if err != nil {
				return outcome{}, err
			}
			if BeingDeleted(name) {
				return outcome{refusal: &workspaceaccess.Refusal{
					Status: http.StatusConflict, Message: AlreadyDeleting,
				}}, nil
			}
			others, err := queries.OtherWorkspaceCount(ctx, OtherWorkspaceCountParams{
				UserID: caller.UserID, WorkspaceID: workspaceID,
			})
			if err != nil {
				return outcome{}, err
			}
			if others == 0 {
				return outcome{refusal: &workspaceaccess.Refusal{
					Status: http.StatusConflict, Message: LastWorkspace,
				}}, nil
			}
			if err := queries.RenameWorkspace(ctx, RenameWorkspaceParams{
				ID: workspaceID, Name: MarkedName(name),
			}); err != nil {
				return outcome{}, err
			}
			return outcome{previousName: name}, nil
		})
	if err != nil {
		return "", nil, err
	}
	return result.previousName, result.refusal, nil
}

func (r Route) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the workspace could not be deleted", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
