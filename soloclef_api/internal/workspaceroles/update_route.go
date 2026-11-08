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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/permissioncatalog"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// UpdatePath is the partial update of one role
// (app/routes/rbac_routes.py:update_role).
const UpdatePath = "/api/v1/workspaces/{workspace_id}/roles/{role_id}"

// RolesUpdate is Permission.ROLES_UPDATE.
const RolesUpdate = "roles:update"

// The Python route's own wording, word for word.
const (
	updateDenied       = "You don't have permission to update roles"
	ownerRoleImmutable = "The Owner role cannot be changed"
	systemRoleLimited  = "Cannot modify name, description, or default status of system roles"
)

// UpdateRoute serves the partial update.
//
// It owes nothing outside its own rows, as create_role does: no Celery message,
// no notification, no PostHog capture. What it adds over create_role is a
// partial body — a field the request did not mention is not written and is not
// checked — and five refusals whose order is the Python route's.
type UpdateRoute struct {
	// Pool is taken rather than a store because the write opens the
	// transaction: the role is read, a name clash looked for, the workspace's
	// default taken off whatever holds it and the row written, all on one
	// pgx.Tx.
	Pool   *pgxpool.Pool
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r UpdateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-workspace-role",
		Method:      http.MethodPut,
		Path:        UpdatePath,
		Summary:     "Change one role of a workspace",
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/workspaces/{workspace_id}/roles/{role_id}, byte for " +
			"byte, and the same rows.",
		Tags: []string{"roles"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
		RoleID      string `path:"role_id" doc:"The role's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r UpdateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// write if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	// The whole request is validated before anything is read or written, and
	// every bad part of it is reported at once, with the path's failures first.
	ids, pathFailures, rangeErr := parsePath(ctx, "workspace_id", "role_id")
	changes, bodyFailures, decodeErr := ReadChanges(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		// The middleware forwards such a body before the gate, so reaching
		// here means the two disagreed.
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}
	if failures := append(pathFailures, bodyFailures...); len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	if rangeErr != nil {
		// Python reads an id of any width and fails when the query runs, which
		// is after the body has been validated.
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}
	workspaceID, roleID := ids[0], ids[1]

	actor, refusal, err := checkGates(ctx.Context(), r.Access, caller, workspaceID, RolesUpdate, updateDenied)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's standing in the workspace", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	// Asked before the role is loaded, exactly as the Python route asks it: a
	// member who may not write roles at all is refused whether or not the role
	// they named exists, so the route is not a role-existence oracle for them.
	if !MayWriteRole(actor, changes.RequestedPermissions()) {
		r.Errors.Write(response, http.StatusForbidden, writeDenied, requestID)
		return
	}

	updated, refusal, err := r.update(ctx.Context(), workspaceID, roleID, changes)
	if err != nil {
		r.fail(ctx, response, requestID, "could not update the role", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	body, err := pythonjson.Marshal(Build(updated))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the role", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// update is the transaction. Every refusal decided from rows is returned from
// inside it rather than raised past it, so the role is read and written in one
// transaction and nothing a refusal passes over is left behind.
//
// The order of the four refusals is the Python route's and is load-bearing: a
// role that is absent is reported before the Owner role is found unchangeable,
// that before a system role's restricted fields, that before a name clash, and
// that before an unknown permission. A request with two faults names the same
// one on both tiers.
func (r UpdateRoute) update(ctx context.Context, workspaceID, roleID int64, changes Changes) (Role, *workspaceaccess.Refusal, error) {
	type outcome struct {
		role    Role
		refusal *workspaceaccess.Refusal
	}
	refuse := func(status int, message string) (outcome, error) {
		return outcome{refusal: &workspaceaccess.Refusal{Status: status, Message: message}}, nil
	}

	result, err := database.Write(ctx, r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			stored, err := RoleIn(ctx, tx, workspaceID, roleID)
			if err != nil {
				return outcome{}, err
			}
			if stored == nil {
				return refuse(http.StatusNotFound, roleAbsent)
			}
			if GrantsFullAccess(*stored) {
				return refuse(http.StatusBadRequest, ownerRoleImmutable)
			}
			if stored.IsSystemRole && changes.TouchesSystemRoleFields() {
				return refuse(http.StatusBadRequest, systemRoleLimited)
			}

			// Only asked when the body names a name that differs from the
			// stored one, which is why renaming a role to what it already is
			// never collides with itself.
			if changes.Name.Present && !sameName(changes.Name.Value, stored.Name) {
				taken, err := NameTakenElsewhere(ctx, tx, workspaceID, changes.Name.Value)
				if err != nil {
					return outcome{}, err
				}
				if taken {
					return refuse(http.StatusConflict,
						"A role with name '"+*changes.Name.Value+"' already exists")
				}
			}

			if changes.Permissions.Present {
				if unknown, found := UnknownPermission(changes.Permissions.Value); found {
					return refuse(http.StatusBadRequest, "Invalid permission: "+unknown)
				}
			}

			// Inside the same transaction as the write, or a role that fails to
			// store leaves the workspace with no default role at all.
			if changes.AsksToBeDefault() && !stored.IsDefault {
				if err := ClearOtherDefaults(ctx, tx, workspaceID); err != nil {
					return outcome{}, err
				}
			}

			// A body that mentions nothing writes nothing: the Python route's
			// session has no changed attribute to flush, so it emits no UPDATE
			// and answers with the row as it stands.
			if !changes.Name.Present && !changes.Description.Present &&
				!changes.Permissions.Present && !changes.IsDefault.Present {
				return outcome{role: *stored}, nil
			}
			role, err := Update(ctx, tx, workspaceID, roleID, changes)
			if err != nil {
				return outcome{}, err
			}
			return outcome{role: role}, nil
		})
	if err != nil {
		return Role{}, nil, err
	}
	return result.role, result.refusal, nil
}

// sameName is `update_data["name"] != db_role.name`, with the explicit null the
// Python comparison also makes: None is never the stored name, because the
// column cannot hold it.
func sameName(requested *string, stored string) bool {
	return requested != nil && *requested == stored
}

// GrantsFullAccess is app/workspace_roles/grants.py:grants_full_access, which
// is what makes the seeded Owner role unchangeable and undeletable through
// these routes whoever asks.
func GrantsFullAccess(role Role) bool {
	for _, permission := range role.Permissions {
		if permission == permissioncatalog.FullAccess {
			return true
		}
	}
	return false
}

func (r UpdateRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the role could not be updated", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
