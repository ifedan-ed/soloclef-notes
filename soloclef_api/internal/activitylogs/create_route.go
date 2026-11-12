package activitylogs

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CreatePath writes a log entry.
const CreatePath = "/api/v1/logs"

// CreateRoute serves the write. The entry is the caller's: nothing in the
// request says whose log it goes into.
type CreateRoute struct {
	Logs   Logs
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock created_at comes from. Overridable for tests.
	Now func() time.Time
}

func (r CreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-log",
		Method:      http.MethodPost,
		Path:        CreatePath,
		Summary:     "Write a log entry",
		Description: "Appends an entry to the caller's own activity log.",
		Tags:        []string{"logs"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r CreateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	requested, failures, decodeErr := ReadRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		// The middleware forwards such a body before the gate, so reaching
		// here means the two disagreed.
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	created, err := r.Logs.Insert(ctx.Context(), caller.UserID, requested, now().UTC())
	if err != nil {
		r.fail(ctx, response, requestID, "could not write the log", err)
		return
	}

	rendered, err := Build(created)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be written")
		return
	}
	body, err := marshal(rendered)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be written")
		return
	}
	pythonjson.Send(response, http.StatusOK, []byte(body))
}

func (r CreateRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	logFailure(ctx, r.Logger, requestID, reason, cause, "the log could not be written")
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
