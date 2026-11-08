package markdownmemory

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
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

// The three routes a workspace's shared memory is reached by.
const (
	TeamReadPath  = "/api/v1/workspaces/{workspace_id}/memory"
	TeamWritePath = "/api/v1/workspaces/{workspace_id}/memory"
	TeamResetPath = "/api/v1/workspaces/{workspace_id}/memory/reset"
)

// Access reads the caller's standing in the workspace. The team routes are
// gated by check_workspace_access rather than check_permission: membership
// alone is enough, and no particular permission is asked for — so any member
// may rewrite the team's memory.
type Access interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*workspaceaccess.Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (workspaceaccess.Workspace, error)
}

// TeamRoutes serves a workspace's shared memory.
type TeamRoutes struct {
	Pool   *pgxpool.Pool
	Memory Store
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r TeamRoutes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-team-memory",
		Method:      http.MethodGet,
		Path:        TeamReadPath,
		Summary:     "A workspace's shared memory document",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/workspaces/{workspace_id}/memory, byte for byte.",
		Tags: []string{"memory"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveRead}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "write-team-memory",
		Method:      http.MethodPut,
		Path:        TeamWritePath,
		Summary:     "Replace a workspace's shared memory document",
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/workspaces/{workspace_id}/memory, byte for byte, and " +
			"the same column.",
		Tags: []string{"memory"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveWrite}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "reset-team-memory",
		Method:      http.MethodPost,
		Path:        TeamResetPath,
		Summary:     "Empty a workspace's shared memory document",
		Description: "The same response as the Python tier's " +
			"POST /api/v1/workspaces/{workspace_id}/memory/reset, byte for byte.",
		Tags: []string{"memory"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveReset}, nil
	})
}

func (r TeamRoutes) serveRead(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	workspaceID, ok := r.admit(ctx, response, requestID, nil)
	if !ok {
		return
	}
	memory, err := r.Memory.TeamMemory(ctx.Context(), workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the team's memory", err)
		return
	}
	r.send(ctx, response, requestID, memory)
}

func (r TeamRoutes) serveWrite(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	requested, failures, decodeErr := ReadRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}
	workspaceID, ok := r.admit(ctx, response, requestID, failures)
	if !ok {
		return
	}
	r.save(ctx, response, requestID, workspaceID, requested)
}

func (r TeamRoutes) serveReset(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	workspaceID, ok := r.admit(ctx, response, requestID, nil)
	if !ok {
		return
	}
	r.save(ctx, response, requestID, workspaceID, "")
}

func (r TeamRoutes) save(ctx huma.Context, response http.ResponseWriter, requestID string, workspaceID int64, requested string) {
	outcome, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Outcome, error) {
			stored, err := StoredTeamMemory(ctx, tx, workspaceID)
			if err != nil {
				return Outcome{}, err
			}
			outcome := Save(TeamScope, requested, stored)
			if !outcome.Write {
				return outcome, nil
			}
			return outcome, WriteTeamMemory(ctx, tx, workspaceID, outcome.Store)
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not write the team's memory", err)
		return
	}
	if outcome.Refusal != "" {
		r.Errors.Write(response, http.StatusBadRequest, outcome.Refusal, requestID)
		return
	}
	r.send(ctx, response, requestID, outcome.Answer)
}

// admit reads the workspace id and runs check_workspace_access.
//
// The write's body failures are reported **before** the gate, which is the
// opposite of the user routes beside them and is not a guess: the team routes
// take the body as a route parameter and call check_workspace_access in their
// own code, so FastAPI validates the body first and a bad body from an
// outsider is a 422 there; the user routes put the gate in
// require_session_context, which is a dependency, so an API key with a bad body
// is a 403 there. Both were asked of the running tier.
func (r TeamRoutes) admit(ctx huma.Context, response http.ResponseWriter, requestID string, bodyFailures []errorenvelope.Failure) (int64, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return 0, false
	}

	workspaceID, pathFailure, rangeErr := requestnumber.Parse(
		ctx.Param("workspace_id"), "path", "workspace_id")
	failures := make([]errorenvelope.Failure, 0, 1+len(bodyFailures))
	if pathFailure != nil {
		failures = append(failures, *pathFailure)
	}
	failures = append(failures, bodyFailures...)
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return 0, false
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return 0, false
	}

	membership, err := r.Access.Membership(ctx.Context(), caller.UserID, workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's membership", err)
		return 0, false
	}
	if refusal := workspaceaccess.CheckAccess(membership); refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return 0, false
	}
	workspace, err := r.Access.Workspace(ctx.Context(), workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the workspace", err)
		return 0, false
	}
	if refusal := workspaceaccess.CheckAPIAccess(workspace, caller.IsGated()); refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return 0, false
	}

	return workspaceID, true
}

func (r TeamRoutes) send(ctx huma.Context, response http.ResponseWriter, requestID, memory string) {
	body, err := pythonjson.Marshal(Build(memory))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the memory document", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r TeamRoutes) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	logFailure(ctx, r.Logger, requestID, reason, cause, "the team memory could not be served")
	// app/routes/team_memory_routes.py catches nothing, so every failure here is
	// one Starlette's ServerErrorMiddleware would have written — and that
	// middleware is outside CORSMiddleware, so the 500 carries no CORS
	// headers (internal/corsmirror.Suppress).
	corsmirror.Suppress(ctx.Context())
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
