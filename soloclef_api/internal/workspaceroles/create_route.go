package workspaceroles

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// CreatePath is the write this tier takes over from the Python tier
// (custom/docs/go-api-tier.md).
const CreatePath = "/api/v1/workspaces/{workspace_id}/roles"

// The Python route's own wording, word for word.
const (
	createDenied = "You don't have permission to create roles"
	writeDenied  = "Only the workspace Owner can change roles, and no role can grant full access"
)

// CreateRoute serves the write.
type CreateRoute struct {
	// Pool is taken rather than a store because the write opens the
	// transaction: every statement it runs goes through the pgx.Tx that
	// internal/database.Write hands it.
	Pool   *pgxpool.Pool
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock the created_at comes from, as the Python model's default
	// comes from the application rather than the server. Overridable for tests.
	Now func() time.Time
}

func (r CreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-workspace-role",
		Method:      http.MethodPost,
		Path:        CreatePath,
		Summary:     "Add a custom role to a workspace",
		Description: "The same response as the Python tier's " +
			"POST /api/v1/workspaces/{workspace_id}/roles, byte for byte, and " +
			"the same rows.",
		Tags: []string{"roles"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r CreateRoute) serve(ctx huma.Context) {
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
	// every bad part of it is reported at once: a request with an unreadable id
	// and a missing name is one 422 naming both, with the path's failure first.
	// The body is already in the context, read by the middleware outside the
	// gate (internal/requestbody).
	workspaceID, pathFailure, rangeErr := requestnumber.Parse(
		ctx.Param("workspace_id"), "path", "workspace_id")
	requested, bodyFailures, decodeErr := ReadRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		// The middleware forwards such a body before the gate, so reaching
		// here means the two disagreed.
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	failures := make([]errorenvelope.Failure, 0, 1+len(bodyFailures))
	if pathFailure != nil {
		failures = append(failures, *pathFailure)
	}
	failures = append(failures, bodyFailures...)
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	if rangeErr != nil {
		// Python reads an id of any width and fails when the query runs, which
		// is after the body has been validated — so this is checked here and
		// not before.
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	membership, err := r.Access.Membership(ctx.Context(), caller.UserID, workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's membership", err)
		return
	}
	if refusal := workspaceaccess.CheckMembership(membership, RolesCreate, createDenied); refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}
	workspace, err := r.Access.Workspace(ctx.Context(), workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the workspace", err)
		return
	}
	if refusal := workspaceaccess.CheckAPIAccess(workspace, caller.IsGated()); refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	// The order of the three refusals below is the Python route's and is
	// load-bearing: a member who may not write roles at all is told so before a
	// name clash is looked for, and a name already taken is reported before an
	// unknown permission, so a request with two faults names the same one on
	// both tiers.
	if !MayWriteRole(membership, requested.Permissions) {
		r.Errors.Write(response, http.StatusForbidden, writeDenied, requestID)
		return
	}

	created, refusal, err := r.create(ctx.Context(), workspaceID, requested)
	if err != nil {
		r.fail(ctx, response, requestID, "could not create the role", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	body, err := pythonjson.Marshal(Build(created))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the role", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// create is the transaction. The two refusals it can still raise are inside it
// because they are decided from rows: the name is read and the role is written
// in one transaction, so a name taken between the two is the insert's own
// failure and not a role created under a name that is already there.
func (r CreateRoute) create(ctx context.Context, workspaceID int64, requested Requested) (Role, *workspaceaccess.Refusal, error) {
	now := r.Now
	if now == nil {
		now = time.Now
	}

	type outcome struct {
		role    Role
		refusal *workspaceaccess.Refusal
	}
	result, err := database.Write(ctx, r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			taken, err := NameTaken(ctx, tx, workspaceID, requested.Name)
			if err != nil {
				return outcome{}, err
			}
			if taken {
				return outcome{refusal: &workspaceaccess.Refusal{
					Status: http.StatusConflict,
					Message: "A role with name '" + requested.Name +
						"' already exists in this workspace",
				}}, nil
			}
			if unknown, found := UnknownPermission(requested.Permissions); found {
				return outcome{refusal: &workspaceaccess.Refusal{
					Status:  http.StatusBadRequest,
					Message: "Invalid permission: " + unknown,
				}}, nil
			}

			// Taking the default from whatever holds it has to be inside the
			// same transaction as the insert, or a role that fails to insert
			// leaves the workspace with no default at all. The Python route's
			// session gives it that for free; here it is the seam.
			if requested.IsDefault {
				if err := ClearDefault(ctx, tx, workspaceID); err != nil {
					return outcome{}, err
				}
			}

			role, err := Insert(ctx, tx, workspaceID, requested, now().UTC())
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

func (r CreateRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the role could not be created", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
