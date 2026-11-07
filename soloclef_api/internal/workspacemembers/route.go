package workspacemembers

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
const Path = "/api/v1/workspaces/{workspace_id}/members"

// denialMessage is the Python route's own wording for a member without
// members:view.
const denialMessage = "You don't have permission to view members"

// Members reads the roster.
type Members interface {
	Roster(ctx context.Context, workspaceID int64) (Roster, error)
}

// Access reads the caller's standing in the workspace.
type Access interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*workspaceaccess.Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (workspaceaccess.Workspace, error)
}

// Route serves the member list.
type Route struct {
	Members Members
	Access  Access
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

// Register adds the operation. The path parameter is declared as a string
// because the refusal for an id that is not a whole number has to be the Python
// tier's 422 envelope, word for word, and a schema violation would be answered
// by Huma in its own words instead.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-workspace-members",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Everyone who is a member of a workspace",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/workspaces/{workspace_id}/members, byte for byte.",
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

	membership, err := r.Access.Membership(ctx.Context(), caller.UserID, workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's membership", err)
		return
	}
	if refusal := workspaceaccess.CheckMembership(membership, workspaceaccess.MembersView, denialMessage); refusal != nil {
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

	roster, err := r.Members.Roster(ctx.Context(), workspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the workspace's members", err)
		return
	}
	body, err := pythonjson.Marshal(Build(roster))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the member list", err)
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
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the member list could not be served", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
