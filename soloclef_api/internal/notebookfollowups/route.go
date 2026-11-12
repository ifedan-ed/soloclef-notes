package notebookfollowups

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

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Models opens the model an account's requests run on (chat.Models): the
// questions are written by the model that wrote the answer.
type Models interface {
	For(ctx context.Context, userID string) (model.Model, string, error)
}

// Notebooks is the one statement the route runs (*Queries, from New over
// the pool). It is an interface so the route's refusals can be tested
// without a database; the statement itself is tested against one.
type Notebooks interface {
	NotebookWithSourceTitles(ctx context.Context, arg NotebookWithSourceTitlesParams) (NotebookWithSourceTitlesRow, error)
}

// Route is the route.
type Route struct {
	Notebooks Notebooks
	Models    Models
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

type pathParameters struct {
	NotebookID string `path:"notebook_id"`
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "notebook-followups", Method: http.MethodPost, Path: Path,
		Summary: "Follow-up questions for a notebook chat answer", Tags: []string{"notebooks"},
	}, func(_ context.Context, _ *pathParameters) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) { r.serve(ctx) }}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		if !errors.Is(cause, context.Canceled) {
			r.Logger.ErrorContext(ctx.Context(), "the follow-up questions could not be served",
				slog.String("request_id", requestID), slog.String("reason", reason), slog.Any("error", cause))
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
	// Digits too wide for a bigint name no notebook: they read as 0, which
	// no row has, and the answer is the 404 below.
	if text == "" || err != nil && strings.Trim(text, "0123456789") != "" {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"path", "notebook_id"}, Message: "Input should be a valid integer"}})
		return
	}

	var asked struct {
		Answer *string `json:"answer"`
		Locale *string `json:"locale"`
	}
	raw, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), maxBodyBytes+1))
	if err == nil && len(raw) > maxBodyBytes {
		err = errors.New("the body is too large")
	}
	if err == nil {
		err = json.Unmarshal(raw, &asked)
	}
	var failures []errorenvelope.Failure
	answer := ""
	if asked.Answer != nil {
		answer = strings.TrimSpace(*asked.Answer)
	}
	if err != nil || answer == "" || utf8.RuneCountInString(answer) > maxAnswerChars {
		failures = append(failures, errorenvelope.Failure{
			Location: []string{"body", "answer"}, Message: "An answer of 1 to 50000 characters is required"})
	}
	locale := ""
	if asked.Locale != nil {
		locale = strings.TrimSpace(*asked.Locale)
	}
	if !isLocale(locale) {
		failures = append(failures, errorenvelope.Failure{
			Location: []string{"body", "locale"}, Message: "A locale is letters, digits and hyphens, at most 35 characters"})
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	notebook, err := r.Notebooks.NotebookWithSourceTitles(ctx.Context(), NotebookWithSourceTitlesParams{
		NotebookID: notebookID, UserID: caller.UserID, TitleLimit: shownTitles})
	if err != nil {
		fail("could not read the notebook", err)
		return
	}
	if !notebook.Owned {
		r.Errors.Write(response, http.StatusNotFound, notebookAbsent, requestID)
		return
	}

	// The request's own context: a person who has moved on to another
	// thread no longer wants these, and the call stops with them.
	asking, done := context.WithTimeout(ctx.Context(), modelTimeout)
	defer done()
	unavailable := func(doing string, cause error) {
		if !errors.Is(cause, context.Canceled) {
			r.Logger.WarnContext(ctx.Context(), "the follow-up questions' model failed",
				slog.String("request_id", requestID), slog.String("doing", doing), slog.String("error", cause.Error()))
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, modelUnavailable, requestID)
	}
	chat, _, err := r.Models.For(asking, caller.UserID)
	if err != nil {
		unavailable("opening the account's model", err)
		return
	}
	reply, err := chat.Generate(asking, model.Request{Messages: []model.Message{
		{Role: model.RoleSystem, Content: prompt + languageInstruction(locale)},
		model.User(material(answer, notebook.Titles)),
	}}, func(model.Chunk) {})
	if err != nil {
		unavailable("asking for the questions", err)
		return
	}
	questions := parseQuestions(reply.Message.Content)
	if len(questions) == 0 {
		r.Logger.WarnContext(ctx.Context(), "the model's reply held no follow-up questions",
			slog.String("request_id", requestID))
		r.Errors.Write(response, http.StatusBadGateway, noQuestions, requestID)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(struct {
		Suggestions []string `json:"suggestions"`
	}{questions})
}

// isLocale reports whether text is empty or shaped like a locale tag
// ("en", "pt-BR", "zh_Hant"). It goes into the prompt, so it is held to the
// characters a tag has and nothing a sentence could be written with.
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
