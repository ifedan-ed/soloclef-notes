package workspaceroles

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The two reads beside the write. The role editor reads the list to draw itself
// and reads one role to open it, both behind roles:read.
const (
	ListPath = "/api/v1/workspaces/{workspace_id}/roles"
	GetPath  = "/api/v1/workspaces/{workspace_id}/roles/{role_id}"
)

// readDenied is the Python routes' wording for a member without roles:read,
// shared by both reads exactly as it is there.
const readDenied = "You don't have permission to view roles"

// roleAbsent is get_role's 404, which a role of another workspace gets too.
const roleAbsent = "Role not found"

// Roles reads the stored roles.
type Roles interface {
	Roles(ctx context.Context, workspaceID int64) ([]Role, error)
	Role(ctx context.Context, workspaceID, roleID int64) (*Role, error)
}

// ReadRoutes serves the role list and one role.
type ReadRoutes struct {
	Roles  Roles
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ReadRoutes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-workspace-roles",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "Every role defined in a workspace",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/workspaces/{workspace_id}/roles, byte for byte.",
		Tags: []string{"roles"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveList}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "read-workspace-role",
		Method:      http.MethodGet,
		Path:        GetPath,
		Summary:     "One role of a workspace",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/workspaces/{workspace_id}/roles/{role_id}, byte for byte.",
		Tags: []string{"roles"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
		RoleID      string `path:"role_id" doc:"The role's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveOne}, nil
	})
}

func (r ReadRoutes) serveList(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	workspaceID, ok := r.admit(ctx, response, requestID, nil)
	if !ok {
		return
	}

	roles, err := r.Roles.Roles(ctx.Context(), workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the workspace's roles", err)
		return
	}
	body, err := pythonjson.Marshal(BuildList(roles))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the role list", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r ReadRoutes) serveOne(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	var roleID int64
	workspaceID, ok := r.admit(ctx, response, requestID, &roleID)
	if !ok {
		return
	}

	role, err := r.Roles.Role(ctx.Context(), workspaceID, roleID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the role", err)
		return
	}
	if role == nil {
		r.Errors.Write(response, http.StatusNotFound, roleAbsent, requestID)
		return
	}
	body, err := pythonjson.Marshal(Build(*role))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the role", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// admit reads the path parameters and runs the two gates both reads share. When
// roleID is given, role_id is read as well and both bad parameters are reported
// at once, as FastAPI reports them.
func (r ReadRoutes) admit(ctx huma.Context, response http.ResponseWriter, requestID string, roleID *int64) (int64, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return 0, false
	}

	// The parameters and the gates are the slice's, shared with the two writes
	// (gates.go), so this tier cannot grow two ideas of what a caller may do.
	names := []string{"workspace_id"}
	if roleID != nil {
		names = append(names, "role_id")
	}
	ids, failures, rangeErr := parsePath(ctx, names...)
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return 0, false
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return 0, false
	}
	workspaceID := ids[0]
	if roleID != nil {
		*roleID = ids[1]
	}

	_, refusal, err := checkGates(ctx.Context(), r.Access, caller, workspaceID, RolesRead, readDenied)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's standing in the workspace", err)
		return 0, false
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return 0, false
	}
	return workspaceID, true
}

func (r ReadRoutes) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the roles could not be served", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
