package logsummary

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Path is the route this tier takes over from the Python tier. The proxy sends
// GET requests for exactly this path here; everything else under /api/v1 still
// goes there (custom/docs/go-api-tier.md).
const Path = "/api/v1/logs/workspaces/{workspace_id}/summary"

// denialMessage is the Python route's own wording for a member without
// logs:read.
const denialMessage = "You don't have permission to read logs in this workspace"

// Logs reads the rows in the window.
type Logs interface {
	InWindow(ctx context.Context, workspaceID int64, since time.Time) ([]Row, error)
}

// Access reads the caller's standing in the workspace.
type Access interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*workspaceaccess.Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (workspaceaccess.Workspace, error)
}

// Route serves the summary.
type Route struct {
	Logs   Logs
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is overridable for tests only.
	Now func() time.Time
}

// Register adds the operation. The parameters are declared as strings because
// the refusal for a parameter that is not a whole number has to be the Python
// tier's 422 envelope, word for word, and a schema violation would be answered
// by Huma in its own words instead.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-workspace-log-summary",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Counts of a workspace's logs in a time window",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/logs/workspaces/{workspace_id}/summary, byte for byte.",
		Tags: []string{"logs"},
	}, func(ctx context.Context, _ *struct {
		WorkspaceID string `path:"workspace_id" doc:"The workspace's id"`
		Hours       string `query:"hours" doc:"How many hours back to count; 24 when absent"`
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

	url := ctx.URL()
	parameters, failures, err := readParameters(ctx.Param("workspace_id"), url.Query())
	if err != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", err)
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	membership, err := r.Access.Membership(ctx.Context(), caller.UserID, parameters.WorkspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's membership", err)
		return
	}
	if refusal := workspaceaccess.CheckMembership(membership, workspaceaccess.LogsRead, denialMessage); refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	workspace, err := r.Access.Workspace(ctx.Context(), parameters.WorkspaceID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the workspace", err)
		return
	}
	if refusal := workspaceaccess.CheckAPIAccess(workspace, caller.IsGated()); refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	since, err := Since(now(), parameters.Hours)
	if err != nil {
		r.fail(ctx, response, requestID, "the time window is out of range", err)
		return
	}

	rows, err := r.Logs.InWindow(ctx.Context(), parameters.WorkspaceID, since)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the workspace's logs", err)
		return
	}
	summary, err := Build(rows, parameters.Hours)
	if err != nil {
		r.fail(ctx, response, requestID, "could not summarise the workspace's logs", err)
		return
	}
	body, err := pythonjson.Marshal(summary)
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the summary", err)
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
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the log summary could not be served", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
