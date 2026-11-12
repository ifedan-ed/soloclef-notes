package notebooksearch

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
)

// Search is the chat's document search (*documents.Searcher): one search in
// the process, so a change to how the chat finds a passage reaches this box
// too.
type Search interface {
	SearchScoped(ctx context.Context, userID, query string, scope documents.Scope, topK int) ([]documents.Hit, documents.Outcome, error)
}

// Notebooks is the one statement the route runs (*Queries, from New over
// the pool). It is an interface so the route can be tested without a
// database; the statement itself is tested against one.
type Notebooks interface {
	NotebookDocuments(ctx context.Context, userID string) ([]NotebookDocumentsRow, error)
}

// Route is the route.
type Route struct {
	Notebooks Notebooks
	Search    Search
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

type parameters struct {
	Query string `query:"q"`
	Limit string `query:"limit"`
}

// The answer, in the shape the web and phone apps read.
type (
	notebookHit struct {
		ID        int64     `json:"id"`
		Title     string    `json:"title"`
		Icon      *string   `json:"icon"`
		UpdatedAt time.Time `json:"updated_at"`
		Passages  []passage `json:"passages"`
	}
	answer struct {
		Query     string        `json:"query"`
		Notebooks []notebookHit `json:"notebooks"`
		TookMS    int64         `json:"took_ms"`
	}
)

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "search-inside-notebooks", Method: http.MethodGet, Path: Path,
		Summary: "Passages inside the caller's notebooks that match a query", Tags: []string{"notebooks"},
	}, func(_ context.Context, _ *parameters) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) { r.serve(ctx) }}, nil
	})
}

func (r Route) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r Route) serve(ctx huma.Context) {
	started := r.now()
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		if !errors.Is(cause, context.Canceled) {
			r.Logger.ErrorContext(ctx.Context(), "the search inside notebooks could not be served",
				slog.String("request_id", requestID), slog.String("reason", reason), slog.Any("error", cause))
		}
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
	}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		fail("the route ran without a caller", nil)
		return
	}

	var failures []errorenvelope.Failure
	query := strings.TrimSpace(ctx.Query("q"))
	if utf8.RuneCountInString(query) > maxQueryChars || strings.ContainsRune(query, 0) {
		failures = append(failures, errorenvelope.Failure{
			Location: []string{"query", "q"}, Message: "A query is at most 200 characters"})
	}
	limit := maxNotebooks
	if text := ctx.Query("limit"); text != "" {
		value, err := strconv.Atoi(text)
		if err != nil || value < 1 || value > maxNotebooks {
			failures = append(failures, errorenvelope.Failure{
				Location: []string{"query", "limit"}, Message: "A limit is a whole number from 1 to 8"})
		}
		limit = value
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	send := func(hits []notebookHit) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(response).Encode(answer{Query: query, Notebooks: hits, TookMS: r.now().Sub(started).Milliseconds()})
	}
	terms := queryTerms(query)
	if utf8.RuneCountInString(query) < minQueryChars {
		send([]notebookHit{})
		return
	}

	// The request's own context: the box asks again on every keystroke, and
	// the search for a query already replaced stops with its request.
	searching, done := context.WithTimeout(ctx.Context(), searchTimeout)
	defer done()
	rows, err := r.Notebooks.NotebookDocuments(searching, caller.UserID)
	if err != nil {
		fail("could not read the notebooks' documents", err)
		return
	}
	if len(rows) == 0 {
		send([]notebookHit{})
		return
	}
	// The rows are ordered by notebook and then document, so each
	// document's links come out in notebook order, and the scope's ids
	// are collected once each.
	links := map[int64][]link{}
	cards := map[int64]NotebookDocumentsRow{}
	scope := make([]int64, 0, len(rows))
	for _, row := range rows {
		if _, seen := links[row.DocumentID]; !seen {
			scope = append(scope, row.DocumentID)
		}
		links[row.DocumentID] = append(links[row.DocumentID], link{
			notebookID: row.NotebookID, sourceID: row.SourceID, folderName: row.FolderName, reach: row.Reach})
		cards[row.NotebookID] = row
	}

	// Limited, so that the search can never read past the notebooks'
	// documents: the scope is a hard limit, as a notebook chat's is.
	hits, _, err := r.Search.SearchScoped(searching, caller.UserID, query,
		documents.Scope{Limited: true, Limit: scope}, searchDocuments)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			r.Logger.WarnContext(ctx.Context(), "the search inside notebooks failed",
				slog.String("request_id", requestID), slog.String("error", err.Error()))
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, searchUnavailable, requestID)
		return
	}

	groups := groupPassages(ranked(hits, terms), links, limit, maxPerNotebook, maxPassages)
	out := make([]notebookHit, 0, len(groups))
	for _, g := range groups {
		card := cards[g.notebookID]
		hit := notebookHit{ID: card.NotebookID, Title: card.Title, Icon: card.Icon,
			// UTC, as every other route writes a time: pgx hands back the
			// server's zone.
			UpdatedAt: card.UpdatedAt.UTC(),
			Passages:  make([]passage, 0, len(g.passages))}
		for _, p := range g.passages {
			hit.Passages = append(hit.Passages, rendered(p, terms))
		}
		out = append(out, hit)
	}
	send(out)
}
