package deliverablejobs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ReadPath is the job card's read.
const ReadPath = "/api/v1/deliverable-jobs/{job_id}"

// ReadRoute answers one of the caller's deliverable jobs as its card shows
// it: kind, title, status, phase, progress, failure and the artifact it
// made.
//
// Why it exists beside Zero: the web reads the job through Zero's
// `deliverableJobs` query, but the phone app also runs on polling (a build
// with Zero off, or a phone whose zero-cache refused it), and polling has
// nothing to read but this. The Python tier answered it under a workspace;
// a job is the account's now, so the path has no workspace and the lookup
// names the owner: another account's job is the same 404 as one never made.
type ReadRoute struct {
	Database DBTX
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

// Register mounts the route.
func (r ReadRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-deliverable-job", Method: http.MethodGet, Path: ReadPath,
		Summary: "One deliverable being made, as its card shows it", Tags: []string{"studio"},
	}, func(_ context.Context, _ *struct {
		JobID string `path:"job_id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ReadRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		// Unreachable behind the gate; a 500 rather than an answer to
		// nobody if the wiring is ever wrong.
		r.Logger.ErrorContext(ctx.Context(), "the deliverable job read ran without a caller", slog.String("request_id", requestID))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	id, err := strconv.ParseInt(ctx.Param("job_id"), 10, 64)
	if err != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"path", "job_id"}, Message: "Input should be a valid integer"}})
		return
	}
	job, err := New(r.Database).Job(ctx.Context(), JobParams{ID: id, UserID: caller.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		r.Errors.Write(response, http.StatusNotFound, "Deliverable job not found", requestID)
		return
	}
	if err != nil {
		r.Logger.ErrorContext(ctx.Context(), "a deliverable job could not be read",
			slog.String("request_id", requestID), slog.Any("error", err))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	// A job moves every few seconds while it runs: a cached copy would show
	// a card stuck at an old phase.
	response.Header().Set("Cache-Control", "private, no-store")
	send(response, job)
}
