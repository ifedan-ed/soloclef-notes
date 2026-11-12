package studiooutputs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Remover removes a document and everything stored for it
// (documentdeletion.Removal).
type Remover interface {
	Remove(ctx context.Context, documentID int64) error
}

// removalTimeout bounds the removal of a deleted output's artifact, which
// runs on past a closed tab (see remove); it is the artifact delete's figure.
const removalTimeout = 2 * time.Minute

// Routes is the three routes.
type Routes struct {
	Pool    *pgxpool.Pool
	Removal Remover
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

type (
	notebookPath struct {
		NotebookID string `path:"notebook_id"`
	}
	outputPath struct {
		NotebookID string `path:"notebook_id"`
		OutputID   string `path:"output_id"`
	}
)

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	mount[notebookPath](api, r, http.MethodGet, OutputsPath, "list-studio-outputs", "A notebook's studio outputs", r.list)
	mount[outputPath](api, r, http.MethodPatch, OutputPath, "rename-studio-output", "Rename a studio output", r.rename)
	mount[outputPath](api, r, http.MethodDelete, OutputPath, "delete-studio-output", "Stop a studio output and remove it", r.remove)
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

func mount[P any](api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"studio"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
			caller, ok := principal.FromContext(ctx.Context())
			if !ok {
				x.fail("the route ran without a caller", nil)
				return
			}
			x.userID = caller.UserID
			serve(x)
		}}, nil
	})
}

// ids reads the path's whole numbers; a value that is not one is a 422.
// An id too wide for a bigint names nothing, so it reads as 0, which no row
// has.
func (x *exchange) ids(names ...string) ([]int64, bool) {
	out := make([]int64, 0, len(names))
	var failures []errorenvelope.Failure
	for _, name := range names {
		text := x.ctx.Param(name)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil && strings.Trim(text, "0123456789") != "" || text == "" {
			failures = append(failures, errorenvelope.Failure{Location: []string{"path", name}, Message: "Input should be a valid integer"})
		}
		out = append(out, value)
	}
	if len(failures) > 0 {
		x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
		return nil, false
	}
	return out, true
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

func (x *exchange) send(status int, value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(status)
	_ = json.NewEncoder(x.response).Encode(value)
}

func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a studio output route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// outputs is the notebook's outputs (or one of them), or false with the 404
// answered for a notebook that is not the caller's.
func (x *exchange) outputs(notebookID, onlyID int64) ([]output, bool) {
	ctx := x.ctx.Context()
	q := New(x.routes.Pool)
	owned, err := q.NotebookIsOwned(ctx, NotebookIsOwnedParams{NotebookID: notebookID, UserID: x.userID})
	if err != nil {
		x.fail("could not look for the notebook", err)
		return nil, false
	}
	if !owned {
		x.refuse(http.StatusNotFound, notebookAbsent)
		return nil, false
	}
	rows, err := q.Outputs(ctx, OutputsParams{NotebookID: notebookID, UserID: x.userID, OnlyID: onlyID})
	if err != nil {
		x.fail("could not read the notebook's outputs", err)
		return nil, false
	}
	listed := make([]output, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, outputOf(row))
	}
	return listed, true
}

func (r Routes) list(x *exchange) {
	ids, ok := x.ids("notebook_id")
	if !ok {
		return
	}
	if listed, ok := x.outputs(ids[0], 0); ok {
		x.send(http.StatusOK, listed)
	}
}

func (r Routes) rename(x *exchange) {
	ids, ok := x.ids("notebook_id", "output_id")
	if !ok {
		return
	}
	var asked struct {
		Title *string `json:"title"`
	}
	raw, err := io.ReadAll(io.LimitReader(x.ctx.BodyReader(), 64<<10))
	if err == nil {
		err = json.Unmarshal(raw, &asked)
	}
	title := ""
	if asked.Title != nil {
		title = strings.TrimSpace(*asked.Title)
	}
	if err != nil || title == "" || utf8.RuneCountInString(title) > maxTitleChars || strings.ContainsRune(title, 0) {
		x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
			Location: []string{"body", "title"}, Message: "A title of 1 to 200 characters is required"}})
		return
	}
	renamed, err := New(r.Pool).RenameOutput(x.ctx.Context(), RenameOutputParams{
		Title: title, ID: ids[1], NotebookID: ids[0], UserID: x.userID})
	if err != nil {
		x.fail("could not rename the output", err)
		return
	}
	listed, ok := x.outputs(ids[0], ids[1])
	if !ok {
		return
	}
	if renamed == 0 || len(listed) == 0 {
		x.refuse(http.StatusNotFound, outputAbsent)
		return
	}
	x.send(http.StatusOK, listed[0])
}

// remove deletes the output and stops its run in one transaction, then
// removes the artifact the run made, if it made one, before answering.
func (r Routes) remove(x *exchange) {
	ids, ok := x.ids("notebook_id", "output_id")
	if !ok {
		return
	}
	now := r.now()
	document, err := database.Write(x.ctx.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (*int64, error) {
		q := New(tx)
		removed, err := q.DeleteOutput(ctx, DeleteOutputParams{ID: ids[1], NotebookID: ids[0], UserID: x.userID})
		if err != nil {
			return nil, err
		}
		var artifact *int64
		switch {
		case removed.DeliverableJobID != nil:
			artifact, err = q.CancelQueuedJob(ctx, CancelQueuedJobParams{Now: now, ID: *removed.DeliverableJobID, UserID: x.userID})
			if errors.Is(err, pgx.ErrNoRows) {
				// Already finished: nothing to stop, and its artifact stays
				// to be removed.
				artifact, err = q.JobArtifact(ctx, JobArtifactParams{ID: *removed.DeliverableJobID, UserID: x.userID})
			}
		case removed.PodcastID != nil:
			if err = q.CancelPodcast(ctx, CancelPodcastParams{ID: *removed.PodcastID, UserID: x.userID}); err == nil {
				artifact, err = q.PodcastArtifact(ctx, PodcastArtifactParams{ID: *removed.PodcastID, UserID: x.userID})
			}
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// The run is gone; the output was all there was.
			return nil, nil
		}
		if err != nil || artifact == nil {
			return nil, err
		}
		documentID, err := q.MarkArtifactDocumentDeleting(ctx, MarkArtifactDocumentDeletingParams{ArtifactID: *artifact, UserID: x.userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return &documentID, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, outputAbsent)
		return
	}
	if err != nil {
		x.fail("could not delete the output", err)
		return
	}
	if document != nil {
		// Not the request's context: a removal that has begun should finish
		// even if the person closes the tab. The document is marked, so a
		// removal that stops partway is out of search and can be repeated.
		removing, done := context.WithTimeout(context.WithoutCancel(x.ctx.Context()), removalTimeout)
		defer done()
		if err := r.Removal.Remove(removing, *document); err != nil {
			x.routes.Logger.ErrorContext(x.ctx.Context(), "a deleted output's artifact was not fully removed",
				slog.String("request_id", x.requestID), slog.Int64("document_id", *document), slog.String("error", err.Error()))
			// The output is gone and the artifact's document stays marked:
			// it is out of search, and deleting the artifact from the
			// artifacts list carries the removal on.
			x.refuse(http.StatusServiceUnavailable, documentdeletion.Unfinished)
			return
		}
	}
	x.response.WriteHeader(http.StatusNoContent)
}
