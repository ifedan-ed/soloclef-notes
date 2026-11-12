// Package documentsearch serves the search over an account's documents as a
// route: the MCP server's knowledge-base search tool.
//
//	POST /api/v1/documents/search-semantic   the documents that best answer a query, with the passages that matched
//
// It runs no search of its own. The search is the chat's
// (soloclef_assistant/documents.Searcher, handed over as chat.Parts.Documents):
// the same embedding, the same vector store, the same keyword leg and the
// same fusion. An agent searching through MCP and the chat searching for a
// person therefore find the same passages for the same words, and a tuning
// of one is a tuning of both.
//
// # What it does better than the Python
//
//   - **The account searched is the caller's**, from the credential. The
//     Python route took a workspace id in the body.
//   - **A type filter confines the search** to the caller's documents of
//     those types before either leg runs, by the limit a notebook chat uses,
//     so `top_k` is the number of matching documents and not what is left
//     of a wider search.
//   - **A type that does not exist is a 422**, not an empty result.
//   - **No score is answered.** The search fuses two rankings; the number it
//     orders by means nothing on its own, and the Python route's `score`
//     invited comparing it across queries. Each hit carries its `rank`.
//   - **Each passage carries its id and page**, which is what a citation
//     needs.
//   - **A search that fails is logged and answered with the generic 500**;
//     the Python put the exception's text in the response.
package documentsearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
)

// Path is the route.
const Path = "/api/v1/documents/search-semantic"

// Served is the route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodPost + " " + Path} }

// The bounds of a request. Twenty documents is the most the chat's search
// answers whatever it is asked for; a query past a thousand characters is a
// document, not a question.
const (
	defaultTopK   = 5
	maxTopK       = 20
	maxQueryChars = 1000
	maxBodyBytes  = 64 << 10
)

// documentTypes is the `documenttype` enum: what `document_types` may name.
var documentTypes = map[string]bool{
	"EXTENSION": true, "CRAWLED_URL": true, "FILE": true, "YOUTUBE_VIDEO": true,
	"GOOGLE_DRIVE_FILE": true, "NOTE": true, "ARTIFACT": true, "LOCAL_FOLDER_FILE": true,
}

// Searcher is the chat's document search (*documents.Searcher).
type Searcher interface {
	Search(ctx context.Context, userID, query string, scope documents.Scope, topK int) ([]documents.Hit, error)
}

// Route is the route.
type Route struct {
	// Pool reads which of the caller's documents are of the types asked for.
	Pool   *pgxpool.Pool
	Search Searcher
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// asked is the request's body.
type asked struct {
	Query         *string  `json:"query"`
	TopK          *int     `json:"top_k"`
	DocumentTypes []string `json:"document_types"`
}

// The answer.
type (
	results struct {
		Items []hit `json:"items"`
	}
	hit struct {
		DocumentID   int64  `json:"document_id"`
		Title        string `json:"title"`
		DocumentType string `json:"document_type"`
		// Rank is the document's place in the result, 1 the best.
		Rank   int       `json:"rank"`
		Chunks []passage `json:"chunks"`
	}
	passage struct {
		ChunkID  int64  `json:"chunk_id"`
		Content  string `json:"content"`
		Position int64  `json:"position"`
		Page     *int64 `json:"page"`
	}
)

// Register mounts the route.
func (r Route) Register(api huma.API) {
	if r.Search == nil {
		panic("documentsearch: Route needs the document search")
	}
	huma.Register(api, huma.Operation{
		OperationID: "search-documents-semantic", Method: http.MethodPost, Path: Path,
		Summary: "Search the caller's documents", Tags: []string{"documents"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// read checks the body, reporting every problem at once.
func read(raw []byte) (query string, topK int, types []string, failures []errorenvelope.Failure) {
	bad := func(field, message string) {
		location := []string{"body"}
		if field != "" {
			location = append(location, field)
		}
		failures = append(failures, errorenvelope.Failure{Location: location, Message: message})
	}
	var body asked
	if err := json.Unmarshal(raw, &body); err != nil {
		bad("", "The body should be a JSON object with a query")
		return "", 0, nil, failures
	}
	if body.Query != nil {
		query = strings.TrimSpace(*body.Query)
	}
	if query == "" || utf8.RuneCountInString(query) > maxQueryChars || strings.ContainsRune(query, 0) {
		bad("query", "A query of 1 to "+strconv.Itoa(maxQueryChars)+" characters is required")
	}
	topK = defaultTopK
	if body.TopK != nil {
		topK = *body.TopK
		if topK < 1 || topK > maxTopK {
			bad("top_k", "Input should be a whole number from 1 to "+strconv.Itoa(maxTopK))
		}
	}
	seen := map[string]bool{}
	for _, name := range body.DocumentTypes {
		switch {
		case !documentTypes[name]:
			bad("document_types", strconv.Quote(name)+" is not a document type")
		case !seen[name]:
			seen[name] = true
			types = append(types, name)
		}
	}
	return query, topK, types, failures
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
			r.Logger.ErrorContext(ctx.Context(), "the document search route could not be served", attributes...)
		}
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
	}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		fail("the route ran without a caller", nil)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"body"}, Message: "The body could not be read, or is larger than a search request"}})
		return
	}
	query, topK, types, failures := read(raw)
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	// The zero scope searches everything the account has. Types confine it
	// with a hard limit, which the search holds both legs to; an account
	// with no document of those types has nothing to search, and the search
	// says so without asking the vector store.
	var scope documents.Scope
	if len(types) > 0 {
		ids, err := New(r.Pool).DocumentsOfTypes(ctx.Context(), DocumentsOfTypesParams{UserID: caller.UserID, DocumentTypes: types})
		if err != nil {
			fail("could not read the documents of the types asked for", err)
			return
		}
		scope = documents.Scope{Limited: true, Limit: ids}
	}
	hits, err := r.Search.Search(ctx.Context(), caller.UserID, query, scope, topK)
	if err != nil {
		fail("the search failed", err)
		return
	}

	answer := results{Items: make([]hit, 0, len(hits))}
	for index, found := range hits {
		item := hit{DocumentID: found.DocumentID, Title: found.Title, DocumentType: found.DocumentType,
			Rank: index + 1, Chunks: make([]passage, 0, len(found.Chunks))}
		for _, chunk := range found.Chunks {
			item.Chunks = append(item.Chunks, passage{ChunkID: chunk.ID, Content: chunk.Content, Position: chunk.Position, Page: chunk.Page})
		}
		answer.Items = append(answer.Items, item)
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(answer)
}
