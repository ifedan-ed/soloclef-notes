package notebookcopy

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

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Route is the copy.
type Route struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

type notebookPath struct {
	NotebookID string `path:"notebook_id"`
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "copy-notebook", Method: http.MethodPost, Path: Path,
		Summary: "Duplicate a notebook with its sources", Tags: []string{"notebooks"},
	}, func(_ context.Context, _ *notebookPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		if !errors.Is(cause, context.Canceled) {
			attributes := []any{slog.String("request_id", requestID), slog.String("reason", reason)}
			if cause != nil {
				attributes = append(attributes, slog.String("error", cause.Error()))
			}
			r.Logger.ErrorContext(ctx.Context(), "a notebook could not be copied", attributes...)
		}
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
	}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		fail("the route ran without a caller", nil)
		return
	}

	text := ctx.Param("notebook_id")
	notebookID, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		if text == "" || strings.Trim(text, "0123456789") != "" {
			r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
				Location: []string{"path", "notebook_id"}, Message: "Input should be a valid integer"}})
			return
		}
		// All digits and too wide for a bigint: it names no notebook.
		notebookID = 0
	}

	title, ok := readTitle(ctx.BodyReader())
	if !ok {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"body", "title"}, Message: "The body must be a JSON object whose title, if given, is text"}})
		return
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	copied, err := New(r.Pool).CopyNotebook(ctx.Context(), CopyNotebookParams{
		NotebookID: notebookID, UserID: caller.UserID, Title: title, Now: now().UTC(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		r.Errors.Write(response, http.StatusNotFound, notebookAbsent, requestID)
		return
	}
	if err != nil {
		fail("could not copy the notebook", err)
		return
	}

	payload, err := pythonjson.Marshal(describe(copied))
	if err != nil {
		fail("could not write the copy's answer", err)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusCreated)
	_, _ = response.Write(payload)
}

// readTitle is the title the body asks for, trimmed; "" when it asks for
// none (no body, no title, null, or blank), which the statement reads as
// "name it after the original". False for a body that is not a JSON object
// or whose title is not text, or that holds a NUL, which Postgres cannot
// store.
func readTitle(body io.Reader) (string, bool) {
	raw, err := io.ReadAll(io.LimitReader(body, maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		return "", false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "", true
	}
	var asked struct {
		Title *string `json:"title"`
	}
	if json.Unmarshal(raw, &asked) != nil {
		return "", false
	}
	if asked.Title == nil {
		return "", true
	}
	title := strings.TrimSpace(*asked.Title)
	return title, !strings.ContainsRune(title, 0)
}

// describe is the copy as the apps read a notebook, key for key what
// internal/notebooks answers. A copy is never pinned, its title is a
// person's, and it is not about to name itself.
func describe(row CopyNotebookRow) *pythonjson.Object {
	optional := func(text *string) any {
		if text == nil {
			return nil
		}
		return *text
	}
	read := pythonjson.NewObject()
	read.Set("id", row.ID)
	read.Set("title", row.Title)
	read.Set("icon", optional(row.Icon))
	read.Set("created_at", pythonjson.PydanticDateTime(row.CreatedAt))
	read.Set("updated_at", pythonjson.PydanticDateTime(row.UpdatedAt))
	read.Set("source_count", row.SourceCount)
	read.Set("enabled_source_count", row.EnabledSourceCount)
	read.Set("chat_goal", row.ChatGoal)
	read.Set("chat_instructions", optional(row.ChatInstructions))
	read.Set("response_length", row.ResponseLength)
	read.Set("featured", false)
	read.Set("title_source", "user")
	read.Set("title_pending", false)
	return read
}
