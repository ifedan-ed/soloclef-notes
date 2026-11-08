package workspacelogs

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// UpdatePath is the partial update of one log
// (app/routes/logs_routes.py:update_log).
const UpdatePath = "/api/v1/logs/{log_id}"

// UpdateRoute serves the partial update.
type UpdateRoute struct {
	Pool   *pgxpool.Pool
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r UpdateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-log",
		Method:      http.MethodPut,
		Path:        UpdatePath,
		Summary:     "Change one log entry",
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/logs/{log_id}, byte for byte, and the same row.",
		Tags: []string{"logs"},
	}, func(ctx context.Context, _ *struct {
		LogID string `path:"log_id" doc:"The log entry's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r UpdateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	logID, pathFailure, rangeErr := parseLogID(ctx)
	changes, bodyFailures, decodeErr := ReadChanges(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
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
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	// The two statements are one transaction: the row the permission was
	// decided from is the row written, and a refusal in between leaves nothing
	// behind.
	type outcome struct {
		entry   Entry
		refusal *workspaceaccess.Refusal
	}
	result, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			stored, err := EntryIn(ctx, tx, caller.UserID, logID)
			if err != nil {
				return outcome{}, err
			}
			if stored == nil {
				return outcome{refusal: &workspaceaccess.Refusal{
					Status: http.StatusNotFound, Message: logAbsent}}, nil
			}
			refusal, err := checkGates(ctx, workspaceaccess.TransactionReader{Tx: tx}, caller,
				stored.WorkspaceID, workspaceaccess.LogsUpdate, writeDenied)
			if err != nil {
				return outcome{}, err
			}
			if refusal != nil {
				return outcome{refusal: refusal}, nil
			}
			if !changes.Mentions() {
				return outcome{entry: *stored}, nil
			}
			entry, err := Update(ctx, tx, logID, changes)
			if err != nil {
				return outcome{}, err
			}
			return outcome{entry: entry}, nil
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not update the log", err)
		return
	}
	if result.refusal != nil {
		r.Errors.Write(response, result.refusal.Status, result.refusal.Message, requestID)
		return
	}

	rendered, err := Build(result.entry)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be updated")
		return
	}
	body, err := marshal(rendered)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be updated")
		return
	}
	pythonjson.Send(response, http.StatusOK, []byte(body))
}

func (r UpdateRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	logFailure(ctx, r.Logger, requestID, reason, cause, "the log could not be updated")
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
