package sourceguides

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Models opens the model an account's requests run on (chat.Models).
type Models interface {
	For(ctx context.Context, userID string) (model.Model, string, error)
}

// Guides is the two statements the route runs (*Queries, from New over the
// pool). It is an interface so the route's refusals can be tested without a
// database; the statements themselves are tested against one.
type Guides interface {
	SourceForGuide(ctx context.Context, arg SourceForGuideParams) (SourceForGuideRow, error)
	SaveGuide(ctx context.Context, arg SaveGuideParams) (int64, error)
}

// Route is the route.
type Route struct {
	Guides Guides
	Models Models
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

type pathParameters struct {
	NotebookID string `path:"notebook_id"`
	SourceID   string `path:"source_id"`
	Locale     string `query:"locale"`
}

// guide is the answer, in the shape the web and phone apps read.
type guide struct {
	DocumentID int64   `json:"document_id"`
	Title      string  `json:"title"`
	Summary    *string `json:"summary"`
	Status     string  `json:"status"`
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "notebook-source-guide", Method: http.MethodGet, Path: Path,
		Summary: "A notebook source's guide", Tags: []string{"notebooks"},
	}, func(_ context.Context, _ *pathParameters) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) { r.serve(ctx) }}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		if !errors.Is(cause, context.Canceled) {
			r.Logger.ErrorContext(ctx.Context(), "a source guide could not be served",
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
	ids := make([]int64, 2)
	for i, name := range []string{"notebook_id", "source_id"} {
		text := ctx.Param(name)
		value, err := strconv.ParseInt(text, 10, 64)
		// Digits too wide for a bigint name nothing: they read as 0, which
		// no row has, and the answer is the 404 below.
		if text == "" || err != nil && strings.Trim(text, "0123456789") != "" {
			failures = append(failures, errorenvelope.Failure{Location: []string{"path", name}, Message: "Input should be a valid integer"})
		}
		ids[i] = value
	}
	locale := strings.TrimSpace(ctx.Query("locale"))
	if !isLocale(locale) {
		failures = append(failures, errorenvelope.Failure{
			Location: []string{"query", "locale"}, Message: "A locale is letters, digits and hyphens, at most 35 characters"})
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	// The locale's case does not change the language, and must not make a
	// second cache entry.
	locale = strings.ToLower(locale)
	suffix := ""
	if locale != "" {
		suffix = "@" + locale
	}

	source, err := r.Guides.SourceForGuide(ctx.Context(), SourceForGuideParams{
		KeySuffix: suffix, SourceID: ids[1], NotebookID: ids[0], UserID: caller.UserID, BodyLimit: bodyLimit})
	if errors.Is(err, pgx.ErrNoRows) {
		r.Errors.Write(response, http.StatusNotFound, sourceAbsent, requestID)
		return
	}
	if err != nil {
		fail("could not read the source", err)
		return
	}
	send := func(summary *string) {
		status := "ready"
		if summary == nil {
			status = "unavailable"
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(response).Encode(guide{DocumentID: source.DocumentID, Title: source.Title, Summary: summary, Status: status})
	}
	if source.Cached {
		send(&source.Summary)
		return
	}
	if source.Body == "" {
		// Nothing to summarise: a file still being read, an image with no
		// text. Said as a state of the document, and not stored, so the
		// guide is written as soon as there is text.
		send(nil)
		return
	}

	// Not the request's context: a guide is written once per document and
	// kept, so one begun for a person who closed the panel is still worth
	// finishing for the next time they open it. The timeout bounds it.
	writing, done := context.WithTimeout(context.WithoutCancel(ctx.Context()), modelTimeout)
	defer done()
	unavailable := func(doing string, cause error) {
		r.Logger.WarnContext(ctx.Context(), "a source guide's model failed", slog.String("request_id", requestID),
			slog.Int64("document_id", source.DocumentID), slog.String("doing", doing), slog.String("error", cause.Error()))
		r.Errors.Write(response, http.StatusServiceUnavailable, modelUnavailable, requestID)
	}
	chat, _, err := r.Models.For(writing, caller.UserID)
	if err != nil {
		unavailable("opening the account's model", err)
		return
	}
	reply, err := chat.Generate(writing, model.Request{Messages: []model.Message{
		{Role: model.RoleSystem, Content: prompt + languageInstruction(locale)},
		model.User(excerpt(source.Body, shownChars)),
	}}, func(model.Chunk) {})
	if err != nil {
		unavailable("writing the guide", err)
		return
	}
	summary := strings.TrimSpace(reply.Message.Content)
	if runes := []rune(summary); len(runes) > maxSummaryChars {
		summary = strings.TrimSpace(string(runes[:maxSummaryChars]))
	}
	if summary == "" {
		r.Logger.WarnContext(ctx.Context(), "the model wrote an empty source guide",
			slog.String("request_id", requestID), slog.Int64("document_id", source.DocumentID))
		r.Errors.Write(response, http.StatusBadGateway, emptyGuide, requestID)
		return
	}
	if _, err := r.Guides.SaveGuide(writing, SaveGuideParams{
		CacheKey: source.CacheKey, Summary: summary, DocumentID: source.DocumentID, UserID: caller.UserID}); err != nil {
		// The guide exists and the person asked for it: it is answered, and
		// the next open pays for the model again.
		r.Logger.ErrorContext(ctx.Context(), "a source guide could not be kept", slog.String("request_id", requestID),
			slog.Int64("document_id", source.DocumentID), slog.String("error", err.Error()))
	}
	send(&summary)
}

// isLocale reports whether text is empty or shaped like a locale tag
// ("en", "pt-BR", "zh_Hant"). It goes into the prompt and the cache key, so
// it is held to the characters a tag has.
func isLocale(text string) bool {
	if len(text) > maxLocaleChars {
		return false
	}
	for _, r := range text {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
