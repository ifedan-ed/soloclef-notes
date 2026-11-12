package activitysummary

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
)

// Path is the summary of the caller's own log. It sits beside
// /api/v1/logs/{log_id}; the router prefers the literal segment, and
// "summary" was never a log id.
const Path = "/api/v1/logs/summary"

// Logs reads the rows in the window.
type Logs interface {
	InWindow(ctx context.Context, ownerID string, since time.Time) ([]Row, error)
}

// Route serves the summary.
type Route struct {
	Logs   Logs
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is overridable for tests only.
	Now func() time.Time
}

// Register adds the operation. The window is declared as a string so that the
// refusal for one that is not a whole number is this API's own 422 envelope
// and not the framework's.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-activity-log-summary",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Counts of the caller's activity log in a time window",
		Description: "Counts by status, level and source, the tasks still " +
			"running and the most recent failures, over the last `hours` hours.",
		Tags: []string{"logs"},
	}, func(ctx context.Context, _ *struct {
		Hours string `query:"hours" doc:"How many hours back to count; 24 when absent"`
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
	hours, failures, err := readHours(url.Query())
	if err != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", err)
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	since, err := Since(now(), hours)
	if err != nil {
		r.fail(ctx, response, requestID, "the time window is out of range", err)
		return
	}

	rows, err := r.Logs.InWindow(ctx.Context(), caller.UserID, since)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the account's logs", err)
		return
	}
	summary, err := Build(rows, hours)
	if err != nil {
		r.fail(ctx, response, requestID, "could not summarise the account's logs", err)
		return
	}
	body, err := pythonjson.Marshal(summary)
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the summary", err)
		return
	}

	pythonjson.Send(response, http.StatusOK, body)
}

// fail logs the reason and answers with the sanitised 500, which never
// carries the reason outward.
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
