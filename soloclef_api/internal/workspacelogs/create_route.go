package workspacelogs

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// CreatePath writes a log entry (app/routes/logs_routes.py:create_log).
const CreatePath = "/api/v1/logs"

// CreateRoute serves the write. The workspace it is gated on comes from the
// body rather than the path, which is the one thing that makes it different
// from every write handed over before it.
type CreateRoute struct {
	Pool   *pgxpool.Pool
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock created_at comes from, as the Python model's default is
	// evaluated in the application. Overridable for tests.
	Now func() time.Time
}

func (r CreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-log",
		Method:      http.MethodPost,
		Path:        CreatePath,
		Summary:     "Write a log entry",
		Description: "The same response as the Python tier's POST /api/v1/logs, " +
			"byte for byte, and the same row.",
		Tags: []string{"logs"},
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

	requested, failures, outOfRange, decodeErr := ReadRequest(
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
	if outOfRange {
		// Python holds the id and the permission query then fails on it, which
		// is after the body has validated — so this is checked here and not
		// inside the reader.
		r.fail(ctx, response, requestID, "the workspace id is outside the range a request can use", nil)
		return
	}

	refusal, err := checkGates(ctx.Context(), r.Access, caller,
		requested.WorkspaceID, workspaceaccess.LogsCreate, writeDenied)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's standing in the workspace", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	now := r.Now
	if now == nil {
		now = time.Now
	}
	created, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Entry, error) {
			return Insert(ctx, tx, requested, now().UTC())
		})
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
