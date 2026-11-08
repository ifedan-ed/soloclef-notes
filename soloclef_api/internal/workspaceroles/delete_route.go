package workspaceroles

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// DeletePath removes a custom role (app/routes/rbac_routes.py:delete_role).
const DeletePath = "/api/v1/workspaces/{workspace_id}/roles/{role_id}"

// RolesDelete is Permission.ROLES_DELETE.
const RolesDelete = "roles:delete"

// The Python route's own wording, word for word.
const (
	deleteDenied          = "You don't have permission to delete roles"
	systemRoleUndeletable = "System roles cannot be deleted"
)

// deletedMessage is the body the route answers with. It declares no
// response_model, so the dict is rendered by jsonable_encoder and Starlette's
// own json.dumps rather than by pydantic.
const deletedMessage = "Role deleted successfully"

// DeleteRoute serves the removal.
type DeleteRoute struct {
	Pool   *pgxpool.Pool
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-workspace-role",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Remove a custom role from a workspace",
		Description: "The same response as the Python tier's " +
			"DELETE /api/v1/workspaces/{workspace_id}/roles/{role_id}, byte " +
			"for byte, and the same row gone.",
		Tags: []string{"roles"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
		RoleID      string `path:"role_id" doc:"The role's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	ids, failures, rangeErr := parsePath(ctx, "workspace_id", "role_id")
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}
	workspaceID, roleID := ids[0], ids[1]

	actor, refusal, err := checkGates(ctx.Context(), r.Access, caller, workspaceID, RolesDelete, deleteDenied)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's standing in the workspace", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}
	// Not MayWriteRole: delete_role asks only whether the actor owns the
	// workspace, because there are no permissions to weigh. The refusal is the
	// same words all the same.
	if actor == nil || !actor.IsOwner {
		r.Errors.Write(response, http.StatusForbidden, writeDenied, requestID)
		return
	}

	refusal, err = r.delete(ctx.Context(), workspaceID, roleID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not delete the role", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	rendered := pythonjson.NewObject()
	rendered.Set("message", deletedMessage)
	body, err := pythonjson.Marshal(rendered)
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// delete is the transaction: the role is read and removed on one pgx.Tx, so a
// role created under the same id between the two cannot be the one deleted.
func (r DeleteRoute) delete(ctx context.Context, workspaceID, roleID int64) (*workspaceaccess.Refusal, error) {
	refusal, err := database.Write(ctx, r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (*workspaceaccess.Refusal, error) {
			stored, err := RoleIn(ctx, tx, workspaceID, roleID)
			if err != nil {
				return nil, err
			}
			if stored == nil {
				return &workspaceaccess.Refusal{
					Status: http.StatusNotFound, Message: roleAbsent}, nil
			}
			if stored.IsSystemRole {
				return &workspaceaccess.Refusal{
					Status: http.StatusBadRequest, Message: systemRoleUndeletable}, nil
			}
			// A role a membership still points at fails the foreign key here,
			// which is the 500 the Python route's flush gives for the same row.
			return nil, Delete(ctx, tx, workspaceID, roleID)
		})
	if err != nil {
		return nil, err
	}
	return refusal, nil
}

func (r DeleteRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the role could not be deleted", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
