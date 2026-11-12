package studiogenerate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/studiooutputs"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// maxBodyBytes bounds a generate body: its longest fields are a 4000
// character template and a 2000 character focus.
const maxBodyBytes = 64 << 10

// Starter starts an artifact job in the background (*Runner).
type Starter interface {
	Start(job Job)
}

// Routes is the two routes.
type Routes struct {
	Pool   *pgxpool.Pool
	Models Models
	// Runner makes the artifact kinds.
	Runner Starter
	// OpenPodcast opens a podcast at its brief gate with the given shape,
	// through the specialist's own opener; StartDraft approves that brief
	// and queues the draft (podcasts.StartDraft). cmd/api supplies both.
	OpenPodcast func(ctx context.Context, owner deliverables.Owner, brief deliverables.PodcastBrief, request deliverables.PodcastRequest) (int64, error)
	StartDraft  func(ctx context.Context, userID string, podcastID int64, requestID string) error
	// Videos queues a video's render job: the specialist's own queuer.
	Videos deliverables.VideoQueuer
	// SandboxEnabled says this deployment can build the kinds made by code
	// (a report, slides, a data table). Off, they are refused by name.
	SandboxEnabled bool
	// ImageEnabled says this deployment has a model that draws (the chat's
	// image model is named), which an infographic is made with. Off, an
	// infographic is refused when it is asked for, as a sandbox kind is.
	// It used to be accepted, shown as generating, and failed a moment
	// later by the specialist, with nothing telling the person why.
	ImageEnabled bool
	Errors       errorenvelope.Writer
	Logger       *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	switch {
	case r.Pool == nil:
		return errors.New("studiogenerate: the database pool is not set")
	case r.Models == nil:
		return errors.New("studiogenerate: the models are not set")
	case r.Runner == nil:
		return errors.New("studiogenerate: the runner is not set")
	case r.OpenPodcast == nil || r.StartDraft == nil:
		return errors.New("studiogenerate: the podcast opener and its draft are not both set")
	case r.Videos == nil:
		return errors.New("studiogenerate: the video queuer is not set")
	case r.Logger == nil:
		return errors.New("studiogenerate: the logger is not set")
	}
	return nil
}

type notebookPath struct {
	NotebookID string `path:"notebook_id"`
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	mount(api, r, http.MethodPost, GeneratePath, "generate-studio-output", "Start a studio output", r.generate)
	mount(api, r, http.MethodGet, SuggestionsPath, "studio-suggestions", "Ideas for a studio dialog", r.suggestions)
}

type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

func mount(api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"studio"},
	}, func(_ context.Context, _ *notebookPath) (*huma.StreamResponse, error) {
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

// notebookID reads the path's id; a value that is not a whole number is a
// 422. An id too wide for a bigint reads as 0, which no row has.
func (x *exchange) notebookID() (int64, bool) {
	text := x.ctx.Param("notebook_id")
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil && strings.Trim(text, "0123456789") != "" || text == "" {
		x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
			Location: []string{"path", "notebook_id"}, Message: "Input should be a valid integer"}})
		return 0, false
	}
	return value, true
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
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a studio route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// refusal is a request refused for a reason the caller is told.
type refusal struct {
	status  int
	message string
}

func (r *refusal) Error() string { return r.message }

// readSources is the documents a generation reads: the ticked sources'
// when ids is nil, otherwise exactly the named sources'. A named source
// that is not this notebook's refuses the whole request, so a request can
// never pull in a document through an id that only looks like a source.
func readSources(ctx context.Context, q *Queries, userID string, notebookID int64, ids []int64) ([]SourceDocument, error) {
	sources, err := q.ChosenSources(ctx, ChosenSourcesParams{
		NotebookID: notebookID, UserID: userID, OnlyEnabled: ids == nil, SourceIds: ids,
	})
	if err != nil {
		return nil, err
	}
	if ids != nil {
		distinct := map[int64]bool{}
		for _, id := range ids {
			distinct[id] = true
		}
		if len(sources) != len(distinct) {
			return nil, &refusal{http.StatusNotFound, sourceAbsent}
		}
	}
	var documentIDs, folderIDs []int64
	for _, source := range sources {
		if source.DocumentID != nil {
			documentIDs = append(documentIDs, *source.DocumentID)
		}
		if source.FolderID != nil {
			folderIDs = append(folderIDs, *source.FolderID)
		}
	}
	if len(documentIDs)+len(folderIDs) == 0 {
		return nil, &refusal{http.StatusBadRequest, noSources}
	}
	rows, err := q.SourceDocuments(ctx, SourceDocumentsParams{
		UserID: userID, DocumentIds: documentIDs, FolderIds: folderIDs, Most: maxSourceDocuments,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &refusal{http.StatusBadRequest, noSources}
	}
	documents := make([]SourceDocument, len(rows))
	for i, row := range rows {
		documents[i] = SourceDocument{ID: row.ID, Title: row.Title, Text: strings.TrimSpace(row.Body)}
	}
	return documents, nil
}

// toolCallID is the job's idempotency key for a click: namespaced so it can
// never be a chat tool call's id, and random when the client sent no id of
// its own, so that such a click is always a new output.
func toolCallID(notebookID int64, requestID string) (string, error) {
	if requestID == "" {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		requestID = hex.EncodeToString(random)
	}
	return "notebook-studio:" + strconv.FormatInt(notebookID, 10) + ":" + requestID, nil
}

func (r Routes) generate(x *exchange) {
	notebookID, ok := x.notebookID()
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(x.ctx.BodyReader(), maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
			Location: []string{"body"}, Message: "The body could not be read, or is larger than 64 KiB"}})
		return
	}
	request, failures := parseRequest(raw)
	if len(failures) > 0 {
		x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
		return
	}
	outputID, err := r.start(x.ctx.Context(), Caller{UserID: x.userID, RequestID: x.requestID}, notebookID, request,
		func(ctx context.Context, q *Queries) ([]SourceDocument, error) {
			return readSources(ctx, q, x.userID, notebookID, request.SourceIDs)
		})
	if !x.proceed(err, "could not start the generation") {
		return
	}
	x.answer(notebookID, outputID)
}

// Caller is whose generation it is and the request id its log lines carry.
type Caller struct {
	UserID    string
	RequestID string
}

// start begins one output and answers its id. It is the whole of a
// generation after the request is read, so that the dialog's route and the
// report view's "Add" (start_from_report.go) start an output by the same
// steps and differ only in which documents it reads (`read`).
func (r Routes) start(ctx context.Context, who Caller, notebookID int64, request Request,
	read func(context.Context, *Queries) ([]SourceDocument, error)) (int64, error) {
	q := New(r.Pool)
	notebookTitle, err := q.NotebookTitle(ctx, NotebookTitleParams{NotebookID: notebookID, UserID: who.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, &refusal{http.StatusNotFound, notebookAbsent}
	}
	if err != nil {
		return 0, fmt.Errorf("looking for the notebook: %w", err)
	}
	if needsSandbox(request) && !r.SandboxEnabled {
		return 0, &refusal{http.StatusServiceUnavailable, notEnabled}
	}
	if needsImageModel(request) && !r.ImageEnabled {
		return 0, &refusal{http.StatusServiceUnavailable, notEnabled}
	}
	documents, err := read(ctx, q)
	if err != nil {
		return 0, err
	}
	callID, err := toolCallID(notebookID, request.RequestID)
	if err != nil {
		return 0, fmt.Errorf("making the request's id: %w", err)
	}
	// A second click with the same id answers the output the first made.
	if existing, err := q.OutputForToolCall(ctx, OutputForToolCallParams{NotebookID: notebookID, UserID: who.UserID, ToolCallID: callID}); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("looking for an earlier click: %w", err)
	}

	options, err := request.Stored()
	if err != nil {
		return 0, fmt.Errorf("encoding the output's options: %w", err)
	}
	sourceIDs := make([]int64, len(documents))
	for i, document := range documents {
		sourceIDs[i] = document.ID
	}
	made, err := json.Marshal(sourceIDs)
	if err != nil {
		return 0, fmt.Errorf("encoding the output's sources: %w", err)
	}
	output := CreateOutputParams{
		Kind: request.Kind, Title: outputTitle(request, notebookTitle), Options: options,
		SourceDocumentIds: made, NotebookID: notebookID, UserID: who.UserID,
	}
	switch request.Kind {
	case "audio":
		// An audio overview is a podcast run and has no job to carry the
		// click's id, so the output keeps it itself. Only a click that
		// sent an id can be repeated; one that sent none has no key.
		if request.RequestID != "" {
			output.RequestKey = &callID
		}
		return r.startAudio(ctx, who, request, documents, output)
	case "video":
		return r.startVideo(ctx, who, request, documents, output, callID)
	default:
		return r.startArtifact(ctx, who, request, documents, output, callID)
	}
}

// proceed answers a failed step and reports whether the route goes on: a
// refusal as itself, a row that vanished mid-request as the notebook's 404,
// anything else as a 500.
func (x *exchange) proceed(err error, doing string) bool {
	var refused *refusal
	switch {
	case err == nil:
		return true
	case errors.As(err, &refused):
		x.refuse(refused.status, refused.message)
	case errors.Is(err, pgx.ErrNoRows):
		// The notebook was deleted between the first read and the write.
		x.refuse(http.StatusNotFound, notebookAbsent)
	default:
		x.fail(doing, err)
	}
	return false
}

// answer sends the output as the panel's list renders it.
func (x *exchange) answer(notebookID, outputID int64) {
	value, found, err := studiooutputs.One(x.ctx.Context(), x.routes.Pool, x.userID, notebookID, outputID)
	if err != nil || !found {
		x.fail("the output was started and could not be read back", err)
		return
	}
	x.send(http.StatusCreated, value)
}

// thread makes the generation's own hidden thread.
func thread(ctx context.Context, q *Queries, userID string, notebookID int64, title string) (int64, error) {
	if runes := []rune(title); len(runes) > 120 {
		title = string(runes[:120])
	}
	return q.CreateStudioThread(ctx, CreateStudioThreadParams{Title: title, NotebookID: notebookID, UserID: userID})
}

// startAudio opens a podcast shaped by the dialog, approves its brief at
// once and lists it. The podcast is opened before the output is written
// because opening may ask the model proxy for voices; if the draft cannot
// then be queued the podcast is closed again, so nothing is left waiting.
func (r Routes) startAudio(ctx context.Context, who Caller, request Request, documents []SourceDocument, output CreateOutputParams) (int64, error) {
	q := New(r.Pool)
	threadID, err := thread(ctx, q, who.UserID, output.NotebookID, output.Title)
	if err != nil {
		return 0, err
	}
	plan := planAudio(request)
	podcastID, err := r.OpenPodcast(ctx, deliverables.Owner{UserID: who.UserID, ThreadID: threadID}, plan.Brief,
		deliverables.PodcastRequest{Title: output.Title, Source: wholeMaterial(documents), Focus: plan.Focus, Language: plan.Language})
	var declined *deliverables.PodcastRefusal
	if errors.As(err, &declined) {
		return 0, &refusal{http.StatusUnprocessableEntity, declined.Reason}
	}
	if err != nil {
		return 0, err
	}
	if err := r.StartDraft(ctx, who.UserID, podcastID, who.RequestID); err != nil {
		r.Logger.ErrorContext(ctx, "a studio podcast's draft could not be queued", slog.String("request_id", who.RequestID),
			slog.Int64("podcast_id", podcastID), slog.String("error", err.Error()))
		if err := q.CancelUnstartedPodcast(context.WithoutCancel(ctx), CancelUnstartedPodcastParams{ID: podcastID, UserID: who.UserID}); err != nil {
			r.Logger.ErrorContext(ctx, "and the podcast could not be closed", slog.Int64("podcast_id", podcastID), slog.String("error", err.Error()))
		}
		return 0, &refusal{http.StatusServiceUnavailable, "The audio overview could not be queued right now. Please try again."}
	}
	output.PodcastID, output.ThreadID = &podcastID, &threadID
	outputID, err := q.CreateOutput(ctx, output)
	var conflict *pgconn.PgError
	if output.RequestKey == nil || !errors.As(err, &conflict) || conflict.Code != uniqueViolation {
		return outputID, err
	}
	// The same click arrived twice at once and the other request listed
	// its output first (the index on the click's key refused this one).
	// The person asked for one overview, so this request answers the
	// other's output. Its own podcast is closed if it is somehow still at
	// its brief; by now it is normally queued, and is then drafted and left
	// unlisted. That is one voicing wasted in a race of milliseconds, and
	// the price of not holding a lock across the call to the model proxy
	// that opening a podcast makes. A click repeated after the first
	// answered, which is the common case, never gets this far.
	winner, found := q.OutputForToolCall(ctx, OutputForToolCallParams{NotebookID: output.NotebookID, UserID: who.UserID, ToolCallID: *output.RequestKey})
	if found != nil {
		return 0, fmt.Errorf("finding the output of the same click: %w", found)
	}
	if err := q.CancelUnstartedPodcast(context.WithoutCancel(ctx), CancelUnstartedPodcastParams{ID: podcastID, UserID: who.UserID}); err != nil {
		r.Logger.WarnContext(ctx, "a duplicate click's podcast could not be closed", slog.Int64("podcast_id", podcastID), slog.String("error", err.Error()))
	}
	return winner, nil
}

// uniqueViolation is Postgres's code for a row a unique index refused.
const uniqueViolation = "23505"

// startVideo queues the render job through the specialist's queuer and
// lists it. The queuer is idempotent on the click's id, so a click that
// lost a race finds the winner's job and then the winner's output.
func (r Routes) startVideo(ctx context.Context, who Caller, request Request, documents []SourceDocument, output CreateOutputParams, callID string) (int64, error) {
	q := New(r.Pool)
	threadID, err := thread(ctx, q, who.UserID, output.NotebookID, output.Title)
	if err != nil {
		return 0, err
	}
	jobID, err := r.Videos.Queue(ctx, deliverables.Owner{UserID: who.UserID, ThreadID: threadID}, callID, deliverables.VideoRequest{
		// On one line, as the worker stores it.
		Title: output.Title, Brief: oneLine(videoBrief(request)), Material: wholeMaterial(documents),
	})
	if err != nil {
		// The same click, already queued under another thread.
		if existing, found := q.OutputForToolCall(ctx, OutputForToolCallParams{NotebookID: output.NotebookID, UserID: who.UserID, ToolCallID: callID}); found == nil {
			return existing, nil
		}
		return 0, err
	}
	output.DeliverableJobID, output.ThreadID = &jobID, &threadID
	return q.CreateOutput(ctx, output)
}

// startArtifact writes the thread, the job and the output together and then
// hands the job to the runner. The model is looked up first: an account
// whose model is gone is told so now, not by a failed job a minute later,
// and the output records which model writes it.
func (r Routes) startArtifact(ctx context.Context, who Caller, request Request, documents []SourceDocument, output CreateOutputParams, callID string) (int64, error) {
	_, modelName, err := r.Models.For(ctx, who.UserID)
	if err != nil {
		r.Logger.WarnContext(ctx, "a studio output was not started: the account's model is unavailable",
			slog.String("request_id", who.RequestID), slog.String("error", err.Error()))
		return 0, &refusal{http.StatusServiceUnavailable, modelUnavailable}
	}
	if output.GeneratedBy, err = json.Marshal(map[string]string{"model": modelName}); err != nil {
		return 0, err
	}
	stored, err := json.Marshal(map[string]any{"version": 1, "output": request.Kind, "format": artifactKind(request)})
	if err != nil {
		return 0, err
	}
	task := artifactTask(request, output.Title, compileMaterial(documents, artifactMaterialChars))

	type started struct{ outputID, jobID, threadID int64 }
	done, err := database.Write(ctx, r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (started, error) {
		q := New(tx)
		threadID, err := thread(ctx, q, who.UserID, output.NotebookID, output.Title)
		if err != nil {
			return started{}, err
		}
		jobID, err := q.CreateArtifactJob(ctx, CreateArtifactJobParams{
			Title: output.Title, ToolCallID: callID, Request: stored, ThreadID: threadID, UserID: who.UserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The same click, a moment earlier, in another request.
			return started{}, &refusal{http.StatusConflict, alreadyStarting}
		}
		if err != nil {
			return started{}, err
		}
		output.DeliverableJobID, output.ThreadID = &jobID, &threadID
		outputID, err := q.CreateOutput(ctx, output)
		return started{outputID, jobID, threadID}, err
	})
	var raced *refusal
	if errors.As(err, &raced) && raced.status == http.StatusConflict {
		// The winner has usually committed by now: answer its output.
		if existing, found := New(r.Pool).OutputForToolCall(ctx, OutputForToolCallParams{NotebookID: output.NotebookID, UserID: who.UserID, ToolCallID: callID}); found == nil {
			return existing, nil
		}
	}
	if err != nil {
		return 0, err
	}
	job := Job{ID: done.jobID, UserID: who.UserID, ThreadID: done.threadID, Task: task}
	if request.Kind == "infographic" {
		job.Style, job.Orientation = request.Options["style"], request.Options["orientation"]
	}
	// After the commit: the runner claims the row, which must be visible.
	r.Runner.Start(job)
	return done.outputID, nil
}
