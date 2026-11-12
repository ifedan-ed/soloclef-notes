package uploads

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

// Indexing queues a document's index job, and marks the document failed if
// it cannot (indexqueue.Indexing).
type Indexing interface {
	Enqueue(ctx context.Context, documentID int64, logContext produce.LogContext) error
}

// Route serves the upload.
//
// cmd/api must NOT list it in `withBodies`: that reader buffers a whole
// body before the route runs, which is the one thing this route exists not
// to do.
type Route struct {
	Rows Rows
	// Files is the store new files are written to: the configured backend
	// (filestore.Stores.Writer), MinIO by default.
	Files   filestore.Writer
	Parsers parserchoice.Registry
	// Vision decides whether the caller may switch use_vision_llm on.
	Vision   Vision
	Indexing Indexing
	// Limits are the same values the capabilities route reports.
	Limits parserchoice.Limits
	// Stall is how long the client may send nothing before the upload is
	// dropped, and how long the answer may take to be accepted.
	Stall  time.Duration
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "upload-documents",
		Method:      http.MethodPost,
		Path:        Path,
		Summary:     "Upload files as documents",
		Description: "multipart/form-data: optionally parser and use_vision_llm, then one or more files parts. " +
			"The fields must precede the files; the files are streamed to storage as they arrive.",
		Tags: []string{"documents"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// answer is the response body.
type answer struct {
	Message string `json:"message"`
	// DocumentIDs are the documents handed to the indexer by this request:
	// new ones, and existing ones given a new file or tried again.
	DocumentIDs []int64 `json:"document_ids"`
	// DuplicateDocumentIDs are the documents the account already had for
	// files in this request.
	DuplicateDocumentIDs []int64 `json:"duplicate_document_ids"`
	TotalFiles           int     `json:"total_files"`
	PendingFiles         int     `json:"pending_files"`
	SkippedDuplicates    int     `json:"skipped_duplicates"`
}

func (r Route) serve(ctx huma.Context) {
	started := time.Now()
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	reply := replier{logger: r.Logger, errors: r.Errors, ctx: ctx.Context(), response: response, requestID: requestID}

	caller, signedIn := principal.FromContext(ctx.Context())
	if !signedIn {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// upload if the wiring is ever wrong.
		reply.fail("the route ran without an authenticated caller", nil)
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(ctx.Header("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		reply.refuse(refuse(http.StatusBadRequest, NotMultipart))
		return
	}
	body, err := pace(ctx.BodyReader(), response, r.Stall)
	if err != nil {
		reply.fail("the upload cannot be read at the client's pace", err)
		return
	}
	reply.body = body

	upload := &receiving{route: r, caller: caller, body: body}
	refused, err := upload.receive(ctx.Context(), multipart.NewReader(body, parameters["boundary"]))
	if refused == nil && err == nil && len(upload.staged) == 0 {
		refused = refuse(http.StatusBadRequest, NoFiles)
	}
	if refused != nil || err != nil {
		// Whatever was stored before the request was refused is not owned
		// by any row and never will be.
		r.discard(ctx.Context(), requestID, keysOf(upload.staged))
		if err != nil {
			reply.fail("the upload could not be received", err)
			return
		}
		reply.refuse(refused)
		return
	}

	recorded, err := r.Rows.Record(ctx.Context(), Batch{
		UserID: caller.UserID, Parser: upload.chosen.String(), Vision: upload.form.vision, Files: upload.staged,
	})
	if err != nil {
		r.discard(ctx.Context(), requestID, keysOf(upload.staged))
		var deleting *ErrBeingDeleted
		var moved *ErrMovedMeanwhile
		switch {
		case errors.As(err, &deleting):
			reply.refuse(refuse(http.StatusConflict, "%q is being deleted. Upload it again in a moment.", deleting.Name))
		case errors.As(err, &moved):
			reply.refuse(refuse(http.StatusConflict, "%q was changed by another upload while it was uploading. Nothing was saved; upload again.", moved.Name))
		default:
			reply.fail("the upload could not be recorded", err)
		}
		return
	}

	// From here the rows are committed. What is left cannot undo them: the
	// copies that were not kept are removed, and each document's job is
	// queued (a job that cannot be queued marks its document failed).
	result := answer{Message: Uploaded, DocumentIDs: []int64{}, DuplicateDocumentIDs: []int64{}, TotalFiles: len(recorded)}
	var unkept []string
	var bytes int64
	logContext := produce.LogContext{RequestID: requestID, UserID: caller.UserID}
	for index, file := range recorded {
		bytes += upload.staged[index].Size
		if !file.Outcome.keepsFile() {
			unkept = append(unkept, upload.staged[index].Key)
		}
		if !file.Outcome.queues() {
			result.DuplicateDocumentIDs = append(result.DuplicateDocumentIDs, file.DocumentID)
			result.SkippedDuplicates++
			continue
		}
		result.DocumentIDs = append(result.DocumentIDs, file.DocumentID)
		result.PendingFiles++
		// A failure is the document's now: it is marked failed with a
		// reason, the file is stored, and the request has still done what
		// it could. The answer lists the document either way, so the
		// client's status poll shows the failure.
		_ = r.Indexing.Enqueue(ctx.Context(), file.DocumentID, logContext)
	}
	r.discard(ctx.Context(), requestID, unkept)

	r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "files uploaded",
		slog.String("request_id", requestID), slog.String("user_id", caller.UserID),
		slog.String("parser", upload.chosen.String()), slog.Int("files", len(recorded)),
		slog.Int("queued", result.PendingFiles), slog.Int("duplicates", result.SkippedDuplicates),
		slog.Int64("bytes", bytes), slog.Duration("took", time.Since(started)))
	reply.send(result)
}

// receiving is one request's upload as it is read.
type receiving struct {
	route  Route
	caller principal.Principal
	body   *pacedBody

	form fields
	// admitted is set once the fields have been checked: the moment the
	// first file part arrives.
	admitted bool
	chosen   selection.Selection
	staged   []Staged
	names    map[string]bool
}

// receive reads the form in order: fields, then files, each file streamed
// to the store as it arrives. A refusal is the caller's to fix; an error is
// the server's.
func (u *receiving) receive(ctx context.Context, form *multipart.Reader) (*refusal, error) {
	for {
		part, err := form.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			// A body that is not the multipart it claims to be, or a
			// client that stopped sending. Either way there is nobody to
			// blame but the sender.
			return refuse(http.StatusBadRequest, Interrupted), nil
		}
		if part.FormName() != fieldFiles {
			if u.admitted {
				return refuse(http.StatusBadRequest, FieldAfterFile), nil
			}
			if refused := u.form.take(part); refused != nil {
				return refused, nil
			}
			continue
		}
		if !u.admitted {
			if refused, err := u.admit(ctx); refused != nil || err != nil {
				return refused, err
			}
			u.admitted = true
		}
		if refused, err := u.store(ctx, part); refused != nil || err != nil {
			return refused, err
		}
	}
}

// admit checks the fields, once, before the first byte of the first file
// is read. There is nothing to ask about the caller: a signed-in account
// may always add to its own documents.
func (u *receiving) admit(ctx context.Context) (*refusal, error) {
	refusal, err := admitVision(ctx, u.route.Vision, u.form.vision, u.caller.UserID)
	if err != nil {
		return nil, err
	}
	if refusal != "" {
		return refuse(http.StatusBadRequest, "%s", refusal), nil
	}
	chosen, refused := parserchoice.Choose(u.route.Parsers, u.form.parser)
	if refused != nil {
		return refuse(parserchoice.Status, "%s", refused.Message), nil
	}
	u.chosen = chosen
	return nil, nil
}

// store streams one file part to the file store.
//
// Everything that can refuse the file without reading it is checked first,
// so a file the parser does not read costs the sender its headers and
// nothing more.
func (u *receiving) store(ctx context.Context, part *multipart.Part) (*refusal, error) {
	limits := u.route.Limits
	name, refused := uploadedName(part)
	if refused != nil {
		return refused, nil
	}
	if len(u.staged) >= limits.MaxFiles {
		return refuse(http.StatusBadRequest, "An upload carries at most %d files; send the rest in another request", limits.MaxFiles), nil
	}
	if u.names[name] {
		// Two files of one name are one document, and which of them it
		// should hold is not this route's to guess.
		return refuse(http.StatusBadRequest, "%q was sent twice in one upload", name), nil
	}
	if !readAsPicture(u.form.vision, name) {
		unread, err := parserchoice.Reads(u.route.Parsers, u.chosen, name)
		if err != nil {
			return nil, err
		}
		if unread != nil {
			return refuse(parserchoice.Status, "%s", unread.Message), nil
		}
	}

	file, refused, err := stager{rows: u.route.Rows, files: u.route.Files, limits: limits, logger: u.route.Logger}.put(
		ctx, u.body, part, name, part.Header.Get("Content-Type"), Identity(name, u.caller.UserID), u.caller.UserID)
	if refused != nil || err != nil {
		return refused, err
	}
	// Staged before the empty check, so that the empty object is removed
	// with the rest when the request is refused.
	u.staged = append(u.staged, file)
	if u.names == nil {
		u.names = map[string]bool{}
	}
	u.names[name] = true
	if file.Size == 0 {
		return refuse(http.StatusBadRequest, "%q is empty", name), nil
	}
	return nil, nil
}

// identified is the two reads that decide which document a file's storage
// key is built for. Both upload routes' Rows have them.
type identified interface {
	NextDocumentID(ctx context.Context) (int64, error)
	UploadNamed(ctx context.Context, userID, identity string) (*Prior, error)
}

// stager streams one file to the file store, for either upload route.
type stager struct {
	rows   identified
	files  filestore.Writer
	limits parserchoice.Limits
	logger *slog.Logger
}

// put streams the file under the key of the document it belongs to: the
// owner's existing document of this identity, or an id taken for a new one. It
// returns the file as stored, or why it was not. After a refusal or an
// error nothing of this file is in the store (the store keeps nothing of a
// write whose body failed).
func (s stager) put(ctx context.Context, body *pacedBody, content io.Reader, name, contentType, identity, userID string) (Staged, *refusal, error) {
	// Which document the file belongs to decides the id in its key.
	prior, err := s.rows.UploadNamed(ctx, userID, identity)
	if err != nil {
		return Staged{}, nil, err
	}
	file := Staged{Name: name, ContentType: contentType, Identity: identity, Backend: s.files.Name()}
	if prior != nil {
		file.DocumentID, file.Existing = prior.ID, true
	} else if file.DocumentID, err = s.rows.NextDocumentID(ctx); err != nil {
		return Staged{}, nil, err
	}
	if file.Key, err = OriginalKey(userID, file.DocumentID, name); err != nil {
		return Staged{}, nil, err
	}

	stored, err := s.files.PutStream(ctx, file.Key, &capped{file: content, limit: s.limits.MaxFileBytes}, file.ContentType)
	switch {
	case errors.Is(err, errTooLarge):
		return Staged{}, refuse(http.StatusRequestEntityTooLarge, "%q is larger than the %s per-file limit", name, sizeText(s.limits.MaxFileBytes)), nil
	case err != nil && (body.failed != nil || ctx.Err() != nil):
		// The bytes stopped arriving.
		return Staged{}, refuse(http.StatusBadRequest, Interrupted), nil
	case err != nil:
		s.logger.LogAttrs(ctx, slog.LevelError, "an uploaded file could not be stored",
			slog.String("backend", file.Backend), slog.String("error", err.Error()))
		return Staged{}, refuse(http.StatusServiceUnavailable, StorageDown), nil
	}
	file.Size, file.SHA256 = stored.Size, stored.SHA256
	return file, nil, nil
}

// sizeText writes a byte limit the way a person reads one. A real limit is
// a whole number of megabytes; the smaller units keep the sentence true for
// a deployment (or a test) that sets a small one.
func sizeText(bytes int64) string {
	const kilobyte, megabyte = 1 << 10, 1 << 20
	switch {
	case bytes >= megabyte && bytes%megabyte == 0:
		return strconv.FormatInt(bytes/megabyte, 10) + " MB"
	case bytes >= megabyte:
		return strconv.FormatFloat(float64(bytes)/megabyte, 'f', 1, 64) + " MB"
	case bytes >= kilobyte:
		return strconv.FormatFloat(float64(bytes)/kilobyte, 'f', 1, 64) + " KB"
	}
	return strconv.FormatInt(bytes, 10) + " bytes"
}

func keysOf(staged []Staged) []string {
	keys := make([]string, len(staged))
	for index, file := range staged {
		keys[index] = file.Key
	}
	return keys
}

// discard removes stored objects no row owns. A removal that fails leaves
// an object nothing points at: it costs storage and is logged with its key
// so it can be removed by hand, and it does not change the answer.
func discard(ctx context.Context, files filestore.Writer, logger *slog.Logger, requestID string, keys []string) {
	if len(keys) == 0 {
		return
	}
	// Not the request's context: a client that hung up is one of the
	// reasons there is something to remove.
	cleanCtx, done := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer done()
	for _, key := range keys {
		if err := files.Remove(cleanCtx, key); err != nil {
			logger.LogAttrs(ctx, slog.LevelError, "a stored upload that no document owns could not be removed",
				slog.String("request_id", requestID), slog.String("key", key), slog.String("error", err.Error()))
		}
	}
}

func (r Route) discard(ctx context.Context, requestID string, keys []string) {
	discard(ctx, r.Files, r.Logger, requestID, keys)
}

// replier writes the one answer of a request. Both upload routes use it.
type replier struct {
	logger    *slog.Logger
	errors    errorenvelope.Writer
	ctx       context.Context
	response  http.ResponseWriter
	requestID string
	// body is set once the upload is being read; from then on the answer
	// needs a write deadline of its own (pacedBody.answerWithin).
	body *pacedBody
}

func (a replier) ready() {
	if a.body == nil {
		return
	}
	if err := a.body.answerWithin(); err != nil {
		a.logger.LogAttrs(a.ctx, slog.LevelWarn, "the answer's write deadline could not be set",
			slog.String("request_id", a.requestID), slog.String("error", err.Error()))
	}
}

func (a replier) refuse(refused *refusal) {
	a.ready()
	a.errors.Write(a.response, refused.status, refused.message, a.requestID)
}

func (a replier) fail(reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", a.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	a.logger.LogAttrs(a.ctx, slog.LevelError, "an upload failed", attributes...)
	a.ready()
	a.errors.Write(a.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, a.requestID)
}

func (a replier) send(result any) {
	body, err := json.Marshal(result)
	if err != nil {
		a.fail("the answer could not be encoded", err)
		return
	}
	a.ready()
	header := a.response.Header()
	header.Set("Content-Type", "application/json")
	header.Set(requestcontext.Header, a.requestID)
	a.response.WriteHeader(http.StatusOK)
	_, _ = a.response.Write(body)
}
