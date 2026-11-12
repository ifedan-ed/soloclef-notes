package studioviews

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/studiogenerate"
)

// Generator starts studio outputs (studiogenerate.Routes). The reading view
// starts an accepted suggestion through it and asks it which kinds this
// deployment can make, so it never proposes one that would then be refused.
type Generator interface {
	CanMake(kind string) bool
	StartFromReport(ctx context.Context, who studiogenerate.Caller, notebookID int64, asked studiogenerate.FromReport) (int64, error)
}

// Routes is the four routes.
type Routes struct {
	Pool *pgxpool.Pool
	// QuickModel is the stored name of the model that does the account's
	// quick work (modelchoices.Choices.ModelFor with the quick purpose): a
	// proxy name, or a model of the account's own connection. Open builds
	// the client for that name, for that account, without calling it (the
	// chat's Models.OpenFor, the one resolver). The suggestions are a quick
	// model's work: a short list, read by a person waiting on the page.
	QuickModel func(ctx context.Context, userID string) (string, error)
	Open       func(ctx context.Context, userID, name string) (model.Model, error)
	// ModelTimeout bounds one attempt at the suggestions; there are at most
	// two, so a first open of a report waits at most twice this.
	ModelTimeout time.Duration
	Studio       Generator
	Errors       errorenvelope.Writer
	Logger       *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	switch {
	case r.Pool == nil:
		return errors.New("studioviews: the database pool is not set")
	case r.QuickModel == nil || r.Open == nil:
		return errors.New("studioviews: the quick model and its opener are not both set")
	case r.ModelTimeout <= 0:
		return errors.New("studioviews: the model timeout is not set")
	case r.Studio == nil:
		return errors.New("studioviews: the studio generator is not set")
	case r.Logger == nil:
		return errors.New("studioviews: the logger is not set")
	}
	return nil
}

type (
	outputPath struct {
		NotebookID string `path:"notebook_id"`
		OutputID   string `path:"output_id"`
	}
	suggestionPath struct {
		NotebookID   string `path:"notebook_id"`
		OutputID     string `path:"output_id"`
		SuggestionID string `path:"suggestion_id"`
	}
)

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	mount[outputPath](api, r, http.MethodGet, ViewPath, "studio-output-view", "A studio output's prompt and sources", r.view)
	mount[outputPath](api, r, http.MethodGet, ReportViewPath, "studio-report-view", "A report's reading view, with its suggestions", r.reportView)
	mount[suggestionPath](api, r, http.MethodPost, AddPath, "add-report-suggestion", "Make the output a report suggestion proposes", r.add)
	mount[suggestionPath](api, r, http.MethodPost, DismissPath, "dismiss-report-suggestion", "Put a report suggestion away", r.dismiss)
}

type exchange struct {
	ctx        huma.Context
	response   http.ResponseWriter
	requestID  string
	userID     string
	routes     Routes
	notebookID int64
	outputID   int64
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
			if x.ids() {
				serve(x)
			}
		}}, nil
	})
}

// ids reads the notebook's and the output's ids; a value that is not a
// whole number is a 422. An id too wide for a bigint names nothing, so it
// reads as 0, which no row has.
func (x *exchange) ids() bool {
	var failures []errorenvelope.Failure
	read := func(name string) int64 {
		text := x.ctx.Param(name)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil && strings.Trim(text, "0123456789") != "" || text == "" {
			failures = append(failures, errorenvelope.Failure{Location: []string{"path", name}, Message: "Input should be a valid integer"})
		}
		return value
	}
	x.notebookID, x.outputID = read("notebook_id"), read("output_id")
	if len(failures) > 0 {
		x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
		return false
	}
	return true
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

func (x *exchange) send(value any) {
	x.response.Header().Set("Content-Type", "application/json")
	// The view changes as outputs are made and deleted; nothing may keep it.
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(x.response)
	// Titles and headings are a person's text, sent as written: `&` is not
	// spelled \u0026 for a client that is not an HTML page.
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a studio view route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// source is one document an output was made from.
type source struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	DocumentType string `json:"document_type"`
}

// sources is the documents the output was made from that the caller still
// has. What the row holds is a JSON list the Python tier also wrote, so a
// member that is not a whole number is passed over.
func (x *exchange) sources(stored []byte) ([]source, bool) {
	listed := []source{}
	ids := documentIDs(stored)
	if len(ids) == 0 {
		return listed, true
	}
	rows, err := New(x.routes.Pool).SourcesOf(x.ctx.Context(), SourcesOfParams{UserID: x.userID, DocumentIds: ids})
	if err != nil {
		x.fail("could not read the output's sources", err)
		return nil, false
	}
	for _, row := range rows {
		listed = append(listed, source{ID: row.ID, Title: row.Title, DocumentType: row.DocumentType})
	}
	return listed, true
}

func documentIDs(stored []byte) []int64 {
	var members []json.RawMessage
	if json.Unmarshal(stored, &members) != nil {
		return nil
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		var id int64
		if json.Unmarshal(member, &id) == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// ready reads the finished output, or answers the 404 and reports false.
func (x *exchange) ready(absent string) (ReadyOutputRow, bool) {
	row, err := New(x.routes.Pool).ReadyOutput(x.ctx.Context(), ReadyOutputParams{
		OutputID: x.outputID, NotebookID: x.notebookID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, absent)
		return row, false
	}
	if err != nil {
		x.fail("could not read the output", err)
		return row, false
	}
	return row, true
}

// option is one text field of the output's stored options, trimmed.
func option(options []byte, name string) string {
	var fields map[string]json.RawMessage
	var text string
	if json.Unmarshal(options, &fields) != nil || json.Unmarshal(fields[name], &text) != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

// view answers the chip under a viewer: the focus the output was asked with
// and the sources it was made from.
func (r Routes) view(x *exchange) {
	row, ok := x.ready(outputAbsent)
	if !ok {
		return
	}
	sources, ok := x.sources(row.SourceDocumentIds)
	if !ok {
		return
	}
	x.send(struct {
		OutputID   int64    `json:"output_id"`
		ArtifactID int64    `json:"artifact_id"`
		Kind       string   `json:"kind"`
		Prompt     string   `json:"prompt"`
		Sources    []source `json:"sources"`
	}{row.ID, row.ArtifactID, row.Kind, option(row.Options, "focus"), sources})
}

// refusal is a request refused for a reason the caller is told.
type refusal struct {
	status  int
	message string
}

func (r *refusal) Error() string { return r.message }

// proceed answers a failed step as what it is and reports whether to go on.
func (x *exchange) proceed(err error, doing string) bool {
	var refused *refusal
	switch {
	case err == nil:
		return true
	case errors.As(err, &refused):
		x.refuse(refused.status, refused.message)
	default:
		x.fail(doing, err)
	}
	return false
}

// change rewrites the reading view's entry under the artifact's row lock.
// build is handed the entry as it is now (an empty one when the version
// has none) and answers what to store.
//
// The lock is what makes two tabs safe: each reads the entry the other
// committed. The version is checked under it too, because the report may
// have been revised between this request's read and its write, and a
// suggestion about the old text must not be stored against the new.
func (x *exchange) change(report report, build func(current entry) (entry, error)) (entry, error) {
	return database.Write(x.ctx.Context(), x.routes.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (entry, error) {
		q := New(tx)
		locked, err := q.LockArtifactMetadata(ctx, LockArtifactMetadataParams{ArtifactID: report.row.ArtifactID, UserID: x.userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return entry{}, &refusal{http.StatusNotFound, reportAbsent}
		}
		if err != nil {
			return entry{}, err
		}
		if locked.Generation != report.row.Generation {
			return entry{}, &refusal{http.StatusConflict, "The report has been revised. Open it again."}
		}
		current := entry{Generation: locked.Generation, Suggestions: []kept{}}
		if stored := entryOf(locked.Metadata, locked.Generation); stored != nil {
			current = *stored
		}
		next, err := build(current)
		if err != nil {
			return entry{}, err
		}
		encoded, err := next.encoded()
		if err != nil {
			return entry{}, err
		}
		if _, err := q.StoreReportView(ctx, StoreReportViewParams{Entry: encoded, ArtifactID: report.row.ArtifactID, UserID: x.userID}); err != nil {
			return entry{}, err
		}
		return next, nil
	})
}

// changeSuggestion is change for one suggestion, found again under the lock.
func (x *exchange) changeSuggestion(report report, id string, edit func(*kept)) (entry, error) {
	return x.change(report, func(current entry) (entry, error) {
		item := current.find(id)
		if item == nil {
			return entry{}, &refusal{http.StatusNotFound, suggestionAbsent}
		}
		edit(item)
		return current, nil
	})
}

func (r Routes) reportView(x *exchange) {
	report, ok := x.report()
	if !ok {
		return
	}
	stored := entryOf(report.row.Metadata, report.row.Generation)
	if stored != nil && stored.Ready {
		x.answer(report, stored, true)
		return
	}
	var kinds []string
	for _, kind := range suggestionKinds {
		if r.Studio.CanMake(kind) {
			kinds = append(kinds, kind)
		}
	}
	// An interactive report is a page of its own, with no sections to put
	// a card under; and a report with no headings has none either.
	if report.format != formatDocument || len(report.outline.sections) == 0 || len(kinds) == 0 {
		x.answer(report, stored, false)
		return
	}
	made, err := r.suggest(x.ctx.Context(), x.userID, report, kinds)
	if err != nil {
		// The report itself is fine; it opens without suggestions and says
		// so, and the next open asks again.
		r.Logger.WarnContext(x.ctx.Context(), "a report's suggestions could not be written",
			slog.String("request_id", x.requestID), slog.Int64("output_id", report.row.ID), slog.String("error", err.Error()))
		x.answer(report, stored, false)
		return
	}
	saved, err := x.change(report, func(current entry) (entry, error) {
		if current.Ready {
			// Another request stored them first; the reader sees one set.
			return current, nil
		}
		return suggestionsEntry(report.row.Generation, report.language, report.outline, made, current.Feedback), nil
	})
	var refused *refusal
	if errors.As(err, &refused) && refused.status == http.StatusConflict {
		// Revised while the model was writing: what it wrote is about text
		// that is gone. The viewer reloads the report and asks again.
		x.answer(report, nil, false)
		return
	}
	if !x.proceed(err, "could not store the report's suggestions") {
		return
	}
	x.answer(report, &saved, true)
}

// suggest asks the account's quick model for the suggestions.
func (r Routes) suggest(ctx context.Context, userID string, report report, kinds []string) ([]proposal, error) {
	name, err := r.QuickModel(ctx, userID)
	if err != nil {
		return nil, err
	}
	reader, err := r.Open(ctx, userID, name)
	if err != nil {
		return nil, err
	}
	return propose(ctx, reader, report.outline, report.language, kinds, r.ModelTimeout)
}

// suggestionID is the path's id. Ids are short ("g3s1"); anything long is
// not one, and is not searched for.
func (x *exchange) suggestionID() (string, bool) {
	id := x.ctx.Param("suggestion_id")
	if id == "" || len(id) > 64 {
		x.refuse(http.StatusNotFound, suggestionAbsent)
		return "", false
	}
	return id, true
}

func (r Routes) dismiss(x *exchange) {
	id, ok := x.suggestionID()
	if !ok {
		return
	}
	report, ok := x.report()
	if !ok {
		return
	}
	saved, err := x.changeSuggestion(report, id, func(item *kept) { item.State = stateDismissed })
	if x.proceed(err, "could not dismiss the suggestion") {
		x.answer(report, &saved, true)
	}
}

// add starts the output a suggestion proposes and records it on the
// suggestion. A suggestion whose output is already being made (another tab,
// a double click) starts nothing; one whose output failed or was cancelled
// starts a new one.
func (r Routes) add(x *exchange) {
	id, ok := x.suggestionID()
	if !ok {
		return
	}
	report, ok := x.report()
	if !ok {
		return
	}
	ctx := x.ctx.Context()
	stored := entryOf(report.row.Metadata, report.row.Generation)
	item := stored.find(id)
	if item == nil {
		x.refuse(http.StatusNotFound, suggestionAbsent)
		return
	}
	if item.OutputID != nil {
		linked, err := New(r.Pool).LinkedOutputs(ctx, LinkedOutputsParams{
			NotebookID: x.notebookID, UserID: x.userID, OutputIds: []int64{*item.OutputID}})
		if err != nil {
			x.fail("could not look for the suggestion's output", err)
			return
		}
		if len(linked) == 1 && !stopped(linked[0].RunStatus) {
			if item.State != stateAdded {
				saved, err := x.changeSuggestion(report, id, func(item *kept) { item.State = stateAdded })
				if !x.proceed(err, "could not mark the suggestion added") {
					return
				}
				stored = &saved
			}
			x.answer(report, stored, true)
			return
		}
	}

	attempts := item.Attempts
	language := languageName(report.language)
	if item.Kind == "audio" {
		// A podcast's language is a code; every other kind's is a name the
		// task is written with.
		language = report.language
	}
	outputID, err := r.Studio.StartFromReport(ctx, studiogenerate.Caller{UserID: x.userID, RequestID: x.requestID}, x.notebookID,
		studiogenerate.FromReport{
			Kind: item.Kind, Title: firstRunes(item.Title, 200), Focus: suggestionFocus(report, *item), Language: firstRunes(language, 40),
			// Stable for an attempt, so a double click starts one output;
			// new for the next, so adding again after a failure starts another.
			RequestID:   "report" + strconv.FormatInt(report.row.ID, 10) + id + "a" + strconv.Itoa(attempts),
			DocumentIDs: documentIDs(report.row.SourceDocumentIds),
		})
	if status, message, refused := studiogenerate.Refused(err); refused {
		x.refuse(status, message)
		return
	}
	if err != nil {
		x.fail("could not start the suggested output", err)
		return
	}
	saved, err := x.changeSuggestion(report, id, func(item *kept) {
		item.State, item.OutputID, item.Attempts = stateAdded, &outputID, attempts+1
	})
	if x.proceed(err, "could not record the suggestion's output") {
		x.answer(report, &saved, true)
	}
}

// stopped reports whether a run will never finish: it failed, was
// cancelled, or is gone.
func stopped(runStatus string) bool {
	return runStatus == "" || runStatus == "failed" || runStatus == "cancelled"
}

func firstRunes(text string, most int) string {
	if runes := []rune(text); len(runes) > most {
		return string(runes[:most])
	}
	return text
}

// suggestionFocus is the steer an added output is made with: what the card
// said, which section of which report it is about, and how that section
// opens, so the output is about the section and not the whole report.
func suggestionFocus(report report, item kept) string {
	heading, excerpt := item.Heading, ""
	for _, s := range report.outline.sections {
		if s.index == item.Section {
			if heading == "" {
				heading = s.heading
			}
			excerpt = strings.TrimRight(firstRunes(s.body(), 900), " \t\n")
			break
		}
	}
	title := report.outline.title
	if title == "" {
		title = report.row.Title
	}
	parts := []string{
		strings.Trim(item.Title+": "+item.Focus, " :"),
		`Base it on the section "` + heading + `" of the report "` + title + `" and cover that section's content in depth.`,
	}
	if excerpt != "" {
		parts = append(parts, "The section reads:\n"+excerpt)
	}
	// A dialog's focus holds 2000 characters, and this is one.
	return firstRunes(strings.Join(parts, "\n\n"), 2000)
}
