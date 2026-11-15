package citedsources

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Passages is the statement the route runs (*Queries, from New over the
// pool). An interface so the route's refusals are tested without a
// database; the statement itself is tested against one.
type Passages interface {
	CitedPassages(ctx context.Context, arg CitedPassagesParams) ([]CitedPassagesRow, error)
}

// Route is the route.
type Route struct {
	Passages Passages
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

type parameters struct {
	IDs string `query:"ids" doc:"Comma-separated ids of the cited passages, at most 100"`
}

// source is one cited passage as the web app reads it.
type source struct {
	ChunkID    int64  `json:"chunk_id"`
	DocumentID int64  `json:"document_id"`
	Title      string `json:"title"`
	Page       *int64 `json:"page"`
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "cited-sources", Method: http.MethodGet, Path: Path,
		Summary:     "The documents a set of cited passages are in",
		Description: "For each of the caller's passages: its document's id and title, and the page it starts on.",
		Tags:        []string{"documents"},
	}, func(_ context.Context, _ *parameters) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		// Unreachable behind the gate; a 500 rather than an unowned read
		// if the wiring is ever wrong.
		r.Logger.ErrorContext(ctx.Context(), "the cited-sources route ran without a caller", slog.String("request_id", requestID))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}

	ids, problem := parseIDs(ctx.Query("ids"))
	if problem != "" {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{
			{Location: []string{"query", "ids"}, Message: problem}})
		return
	}

	rows, err := r.Passages.CitedPassages(ctx.Context(), CitedPassagesParams{ChunkIds: ids, UserID: caller.UserID})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			r.Logger.ErrorContext(ctx.Context(), "cited sources could not be read",
				slog.String("request_id", requestID), slog.Any("error", err))
		}
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	sources := make([]source, 0, len(rows))
	for _, row := range rows {
		sources = append(sources, source{ChunkID: row.ChunkID, DocumentID: row.DocumentID, Title: row.Title, Page: row.PageStart})
	}
	response.Header().Set("Content-Type", "application/json")
	// The titles are the account's own and change when a document is
	// renamed: kept by no cache between the two.
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(struct {
		Sources []source `json:"sources"`
	}{sources})
}

// parseIDs reads "812,90,5": one to maxIDs whole numbers, each once. It
// returns the sentence a 422 says when the list is not that.
func parseIDs(text string) ([]int64, string) {
	if strings.TrimSpace(text) == "" {
		return nil, "At least one passage id is required"
	}
	fields := strings.Split(text, ",")
	if len(fields) > maxIDs {
		return nil, "At most " + strconv.Itoa(maxIDs) + " passage ids may be asked at once"
	}
	ids := make([]int64, 0, len(fields))
	seen := make(map[int64]bool, len(fields))
	for _, field := range fields {
		id, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64)
		if err != nil || id < 1 {
			return nil, "Each passage id must be a positive whole number"
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, ""
}
