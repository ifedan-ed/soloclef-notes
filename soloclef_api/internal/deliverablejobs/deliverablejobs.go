// Package deliverablejobs serves the two buttons on a deliverable a chat or
// the studio is making (a video):
//
//	POST /api/v1/deliverable-jobs/{job_id}/cancel
//	POST /api/v1/deliverable-jobs/{job_id}/retry
//
// A queued job is cancelled at once; a running one is asked to stop and its
// worker finishes the cancellation. Asking again, or asking about a job
// already stopping, answers the job as it is. A job that has finished, or
// has already saved what it made, can no longer be cancelled: 409.
//
// A failed or cancelled job is retried as its next attempt (retry.go).
package deliverablejobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CancelPath is the route.
const CancelPath = "/api/v1/deliverable-jobs/{job_id}/cancel"

// CancelRoute serves the cancel.
type CancelRoute struct {
	Database DBTX
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Register mounts the route.
func (r CancelRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "cancel-deliverable-job", Method: http.MethodPost, Path: CancelPath,
		Summary: "Stop a deliverable being made", Tags: []string{"studio"},
	}, func(_ context.Context, _ *struct {
		JobID string `path:"job_id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// jobRead is what the job card reads.
type jobRead struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Phase       *string   `json:"phase"`
	Progress    int64     `json:"progress"`
	FailureCode *string   `json:"failure_code"`
	ArtifactID  *int64    `json:"artifact_id"`
	ThreadID    *int64    `json:"thread_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (r CancelRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		r.Logger.ErrorContext(ctx.Context(), "a deliverable job could not be cancelled",
			slog.String("request_id", requestID), slog.String("reason", reason), slog.Any("error", cause))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
	}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		fail("the route ran without a caller", nil)
		return
	}
	id, err := strconv.ParseInt(ctx.Param("job_id"), 10, 64)
	if err != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"path", "job_id"}, Message: "Input should be a valid integer"}})
		return
	}
	q := New(r.Database)
	current, err := q.Job(ctx.Context(), JobParams{ID: id, UserID: caller.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		r.Errors.Write(response, http.StatusNotFound, "Deliverable job not found", requestID)
		return
	}
	if err != nil {
		fail("could not read the job", err)
		return
	}
	switch current.Status {
	case "cancelling", "cancelled":
		send(response, current)
		return
	case "queued", "running":
	default:
		r.Errors.Write(response, http.StatusConflict, "This deliverable can no longer be cancelled", requestID)
		return
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	cancelled, err := q.CancelJob(ctx.Context(), CancelJobParams{Now: now, ID: id, UserID: caller.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		// It moved on between the read and the write (its worker finished
		// it, or saved its artifact): it is answered as it now is.
		current, err = q.Job(ctx.Context(), JobParams{ID: id, UserID: caller.UserID})
		if err != nil {
			fail("could not read the job again", err)
			return
		}
		send(response, current)
		return
	}
	if err != nil {
		fail("could not cancel the job", err)
		return
	}
	send(response, JobRow(cancelled))
}

func send(response http.ResponseWriter, row JobRow) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(jobRead(row))
}
