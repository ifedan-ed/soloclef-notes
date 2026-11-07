package myaccess

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/surfsense/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/principal"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/workspaceaccess"
)

// Path is the route this tier takes over from the Python tier
// (custom/docs/go-api-tier.md).
const Path = "/api/v1/workspaces/{workspace_id}/my-access"

// Access reads the caller's standing in the workspace. This route needs nothing
// else: the two rows the gates already read are the whole answer.
type Access interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*workspaceaccess.Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (workspaceaccess.Workspace, error)
}

// Route serves the caller's access.
type Route struct {
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register adds the operation. The path parameter is declared as a string
// because the refusal for an id that is not a whole number has to be the Python
// tier's 422 envelope, word for word, and a schema violation would be answered
// by Huma in its own words instead.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-my-workspace-access",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "The caller's role and permissions in a workspace",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/workspaces/{workspace_id}/my-access, byte for byte.",
		Tags: []string{"members"},
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
		// read if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	workspaceID, failure, err := requestnumber.Parse(ctx.Param("workspace_id"), "path", "workspace_id")
	if err != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", err)
		return
	}
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}

	// check_workspace_access: a member, with no particular permission, and then
	// the API-access gate. The workspace is only looked up for a caller who is
	// already a member, so this route cannot be used to find out which
	// workspace ids exist.
	membership, err := r.Access.Membership(ctx.Context(), caller.UserID, workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's membership", err)
		return
	}
	if refusal := workspaceaccess.CheckAccess(membership); refusal != nil {
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

	body, err := pythonjson.Marshal(Build(workspaceID, workspace, membership))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the caller's access", err)
		return
	}

	pythonjson.Send(response, http.StatusOK, body)
}

// fail logs the reason and answers with the sanitised 500 the Python tier
// answers with, which never carries the reason outward.
func (r Route) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the caller's access could not be served", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
