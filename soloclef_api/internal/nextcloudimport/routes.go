package nextcloudimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

// Starter takes the downloads an import owes (*Downloads).
type Starter interface {
	Start(jobs []Job, logContext produce.LogContext)
}

// Routes are the picker's two reads (browse.go), the preview, the import
// and the refresh.
//
// cmd/api must not list any of them in `withBodies`: each reads its own
// body, with its own limit.
type Routes struct {
	Rows    Rows
	Parsers parserchoice.Registry
	// Vision decides whether the caller may switch use_vision_llm on
	// (uploadvision.Vision), the rule the upload routes apply.
	Vision interface {
		Refusal(ctx context.Context, userID string) (string, error)
	}
	Credentials nextcloud.Credentials
	// Connect makes the WebDAV client for an account.
	Connect   Connect
	Downloads Starter
	// MaxFileBytes is the upload route's per-file limit: an import is an
	// upload by another road, and the same limit applies.
	MaxFileBytes int64
	Errors       errorenvelope.Writer
	Logger       *slog.Logger
}

func (r Routes) Register(api huma.API) {
	r.registerReads(api)
	for _, route := range []struct {
		id, path, summary string
		serve             func(*asking)
	}{
		{"preview-nextcloud-import", PreviewPath, "What an import of this pick would bring in", r.preview},
		{"import-from-nextcloud", ImportPath, "Import the picked Nextcloud files as documents", r.importPick},
		{"refresh-from-nextcloud", RefreshPath, "Read one imported file again if it changed in Nextcloud", r.refresh},
	} {
		serve := route.serve
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: http.MethodPost, Path: route.path, Summary: route.summary, Tags: []string{"connectors"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				serve(&asking{routes: r, ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())})
			}}, nil
		})
	}
}

// refusal is a request these routes will not carry out: a status and a
// sentence, and for the three the picker branches on, a code.
type refusal struct {
	status  int
	message string
	// code is what the web's import-errors reader matches:
	// "nextcloud_auth", "nextcloud_reconnect", "too_many_files".
	code string
	// extra is added to the envelope's error object.
	extra map[string]any
}

func refuse(status int, format string, arguments ...any) *refusal {
	return &refusal{status: status, message: fmt.Sprintf(format, arguments...)}
}

// asking is one request being answered.
type asking struct {
	routes    Routes
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
}

func (a *asking) refuse(refused *refusal) {
	if refused.code == "" {
		a.routes.Errors.Write(a.response, refused.status, refused.message, a.requestID)
		return
	}
	// The tier's envelope with the route's own code in place of the
	// status's, which is where the web reads a code from.
	now := time.Now
	if a.routes.Errors.Now != nil {
		now = a.routes.Errors.Now
	}
	details := map[string]any{
		"code": refused.code, "message": refused.message, "status": refused.status, "request_id": a.requestID,
		"timestamp": now().UTC().Format(isoTime), "report_url": a.routes.Errors.ReportURL,
	}
	for key, value := range refused.extra {
		details[key] = value
	}
	body, err := json.Marshal(map[string]any{"error": details, "detail": refused.message})
	if err != nil {
		a.fail("a refusal could not be encoded", err)
		return
	}
	a.write(refused.status, body)
}

func (a *asking) fail(reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", a.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	a.routes.Logger.LogAttrs(a.ctx.Context(), slog.LevelError, "a Nextcloud import route failed", attributes...)
	a.routes.Errors.Write(a.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, a.requestID)
}

func (a *asking) send(answer any) {
	body, err := json.Marshal(answer)
	if err != nil {
		a.fail("the answer could not be encoded", err)
		return
	}
	a.write(http.StatusOK, body)
}

func (a *asking) write(status int, body []byte) {
	header := a.response.Header()
	header.Set("Content-Type", "application/json")
	header.Set(requestcontext.Header, a.requestID)
	a.response.WriteHeader(status)
	_, _ = a.response.Write(body)
}

// number reads a path parameter that is a row's id. ok is false when the
// answer has been written; absent is the 404 for a number no row can have.
func (a *asking) number(name, absent string) (int64, bool) {
	id, failure, rangeErr := requestnumber.Parse(a.ctx.Param(name), "path", name)
	if failure != nil {
		a.routes.Errors.WriteValidation(a.response, a.requestID, []errorenvelope.Failure{*failure})
		return 0, false
	}
	if rangeErr != nil || id < 1 {
		a.refuse(refuse(http.StatusNotFound, "%s", absent))
		return 0, false
	}
	return id, true
}

// caller is who is asking. The routes are behind the tier's gate, so there
// is always one; a 500 if the wiring is ever wrong.
func (a *asking) caller() (principal.Principal, bool) {
	caller, signedIn := principal.FromContext(a.ctx.Context())
	if !signedIn {
		a.fail("the route ran without an authenticated caller", nil)
	}
	return caller, signedIn
}

// account is the caller's own connection as an account. A refusal is the
// 404 for a connection that is not theirs, or the 400 that says to connect
// again.
func (r Routes) account(ctx context.Context, connectorID int64, owner string) (Account, *refusal, error) {
	config, found, err := r.Rows.Connection(ctx, connectorID, owner)
	if err != nil {
		return Account{}, nil, err
	}
	if !found {
		return Account{}, refuse(http.StatusNotFound, connectionAbsent), nil
	}
	account, err := r.Credentials.Account(config)
	var reconnect *nextcloud.ReconnectError
	if errors.As(err, &reconnect) {
		return Account{}, &refusal{status: http.StatusBadRequest, code: "nextcloud_reconnect", message: reconnect.Message}, nil
	}
	return account, nil, err
}

// remoteRefusal words a failure of the Nextcloud side. A refused password
// is a 400, never a 401: a 401 from this API signs the person out of the
// app, and it is Nextcloud that refused them, not this server.
func remoteRefusal(err error) *refusal {
	if picked, isSelection := isSelection(err); isSelection {
		return refuse(http.StatusBadRequest, "%s", picked.message)
	}
	switch {
	case errors.Is(err, nextcloud.ErrAuth):
		return &refusal{status: http.StatusBadRequest, code: "nextcloud_auth", message: "Nextcloud refused the username or app password"}
	case errors.Is(err, nextcloud.ErrNotFound):
		return refuse(http.StatusNotFound, "A picked file or folder is no longer in Nextcloud")
	case errors.Is(err, nextcloud.ErrUnavailable):
		return refuse(http.StatusBadGateway, "Nextcloud could not be reached. Try again in a moment.")
	}
	return nil
}

// understood is what the preview and the import both begin with: the
// caller, their connection, the body, and the parser it names. ok is false
// when the answer has been written.
func (a *asking) understood() (caller principal.Principal, connectorID int64, account Account, body request, chosen selection.Selection, ok bool) {
	r := a.routes
	caller, signedIn := a.caller()
	if !signedIn {
		return
	}
	connectorID, numbered := a.number("connector_id", connectionAbsent)
	if !numbered {
		return
	}
	body, wrong := readRequest(a.ctx.BodyReader())
	if wrong != "" {
		a.refuse(refuse(http.StatusUnprocessableEntity, "%s", wrong))
		return
	}
	if body.Vision {
		// Refused, not ignored, when the caller has no vision model: they
		// asked for pictures to be described, and importing them without
		// one would be a different result from the one asked for.
		refusal, err := r.Vision.Refusal(a.ctx.Context(), caller.UserID)
		if err != nil {
			a.fail("the caller's vision model could not be read", err)
			return
		}
		if refusal != "" {
			a.refuse(refuse(http.StatusBadRequest, "%s", refusal))
			return
		}
	}
	chosen, unconfigured := parserchoice.Choose(r.Parsers, body.Parser)
	if unconfigured != nil {
		a.refuse(refuse(parserchoice.Status, "%s", unconfigured.Message))
		return
	}
	account, refused, err := r.account(a.ctx.Context(), connectorID, caller.UserID)
	if err != nil {
		a.fail("the connection could not be read", err)
		return
	}
	if refused != nil {
		a.refuse(refused)
		return
	}
	return caller, connectorID, account, body, chosen, true
}

// plan lists Nextcloud for the pick. ok is false when the answer has been
// written.
func (a *asking) plan(account Account, body request, chosen selection.Selection) (Plan, bool) {
	r := a.routes
	plan, err := planner{remote: r.Connect(account), parsers: r.Parsers, chosen: chosen, maxBytes: r.MaxFileBytes, vision: body.Vision}.
		plan(a.ctx.Context(), body.Picks)
	if err == nil {
		return plan, true
	}
	if refused := remoteRefusal(err); refused != nil {
		// The cause may name the server; it is for the log.
		r.Logger.LogAttrs(a.ctx.Context(), slog.LevelWarn, "a Nextcloud pick could not be listed",
			slog.String("request_id", a.requestID), slog.String("error", err.Error()))
		a.refuse(refused)
		return Plan{}, false
	}
	a.fail("the pick could not be planned", err)
	return Plan{}, false
}

func (r Routes) preview(a *asking) {
	_, _, account, body, chosen, ok := a.understood()
	if !ok {
		return
	}
	plan, ok := a.plan(account, body, chosen)
	if !ok {
		return
	}
	a.send(struct {
		FileCount        int   `json:"file_count"`
		TotalBytes       int64 `json:"total_bytes"`
		UnsupportedCount int   `json:"unsupported_count"`
		TooLargeCount    int   `json:"too_large_count"`
		OverLimit        bool  `json:"over_limit"`
		MaxFiles         int   `json:"max_files"`
	}{len(plan.Files), plan.TotalBytes(), len(plan.Unsupported), len(plan.TooLarge), plan.OverLimit, MaxImportFiles})
}

func (r Routes) importPick(a *asking) {
	caller, connectorID, account, body, chosen, ok := a.understood()
	if !ok {
		return
	}
	ctx := a.ctx.Context()

	// Everything that can refuse the import without asking Nextcloud is
	// decided first: a pick is not listed for a notebook that is not there.
	if body.NotebookID != nil {
		owned, err := r.Rows.OwnsNotebook(ctx, *body.NotebookID, caller.UserID)
		if err != nil {
			a.fail("the notebook could not be read", err)
			return
		}
		if !owned {
			// Another account's notebook and no notebook: one answer.
			a.refuse(refuse(http.StatusNotFound, notebookAbsent))
			return
		}
	}

	plan, ok := a.plan(account, body, chosen)
	if !ok {
		return
	}
	if plan.OverLimit {
		// Nothing of a pick that is too big is imported: which two hundred
		// of its files would be an arbitrary choice.
		a.refuse(&refusal{
			status: http.StatusUnprocessableEntity, code: "too_many_files",
			message: fmt.Sprintf("Pick at most %d files at a time", MaxImportFiles),
			extra:   map[string]any{"max_files": MaxImportFiles},
		})
		return
	}

	started, err := r.Rows.Start(ctx, plan, account, Options{
		ConnectorID: connectorID, UserID: caller.UserID,
		Parser: chosen.String(), Vision: body.Vision, NotebookID: body.NotebookID,
	})
	var deleting *errBeingDeleted
	if errors.As(err, &deleting) {
		a.refuse(refuse(http.StatusConflict, "%q is being deleted. Import it again in a moment.", deleting.name))
		return
	}
	if errors.Is(err, errNotebookGone) {
		// Deleted between the check above and the transaction.
		a.refuse(refuse(http.StatusNotFound, notebookAbsent))
		return
	}
	if err != nil {
		a.fail("the import could not be recorded", err)
		return
	}
	// After the commit: a download ends by writing to its document's row.
	r.Downloads.Start(started.Jobs, produce.LogContext{RequestID: a.requestID, UserID: caller.UserID})
	r.Logger.LogAttrs(ctx, slog.LevelInfo, "Nextcloud import started",
		slog.String("request_id", a.requestID), slog.String("user_id", caller.UserID),
		slog.String("parser", chosen.String()), slog.Int("queued", len(started.Jobs)), slog.Int("reused", started.Reused),
		slog.Int("unsupported", len(plan.Unsupported)), slog.Int("too_large", len(plan.TooLarge)))

	a.send(struct {
		DocumentIDs []int64  `json:"document_ids"`
		FolderIDs   []int64  `json:"folder_ids"`
		Queued      int      `json:"queued"`
		Reused      int      `json:"reused"`
		Unsupported []string `json:"unsupported"`
		TooLarge    []string `json:"too_large"`
	}{listed(started.DocumentIDs), listed(started.FolderIDs), len(started.Jobs), started.Reused, listed(plan.Unsupported), listed(plan.TooLarge)})
}

// listed is a list that is answered [] and never null.
func listed[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func (r Routes) refresh(a *asking) {
	caller, signedIn := a.caller()
	if !signedIn {
		return
	}
	connectorID, ok := a.number("connector_id", connectionAbsent)
	if !ok {
		return
	}
	documentID, ok := a.number("document_id", importedAbsent)
	if !ok {
		return
	}
	ctx := a.ctx.Context()
	account, refused, err := r.account(ctx, connectorID, caller.UserID)
	if err != nil {
		a.fail("the connection could not be read", err)
		return
	}
	if refused != nil {
		a.refuse(refused)
		return
	}

	// The caller's own document, or nothing: another account's document is
	// read as absent by the statement itself.
	document, err := r.Rows.Imported(ctx, documentID, caller.UserID)
	if err != nil {
		a.fail("the document could not be read", err)
		return
	}
	// The document must also have come from this Nextcloud account on this
	// server: the path in its facts is only meaningful there. The
	// connection's id is not compared: a person who removed the connection
	// and made it again has a new row for the same account, and the file is
	// still theirs to refresh.
	if document == nil || document.State == "deleting" || document.Path == "" ||
		document.Username != account.Username || document.ServerURL != account.BaseURL {
		a.refuse(refuse(http.StatusNotFound, importedAbsent))
		return
	}
	queued := struct {
		Status string `json:"status"`
	}{"queued"}
	// Already on its way: fetching or indexing. A second job would race
	// the first. An abandoned download is not on its way (store.go).
	abandoned := document.State == "pending" && document.Stage == "downloading" && document.Stale
	if (document.State == "pending" || document.State == "processing") && !abandoned {
		a.send(queued)
		return
	}

	path, err := nextcloud.NormalizePath(document.Path)
	if err != nil {
		a.refuse(refuse(http.StatusNotFound, importedAbsent))
		return
	}
	entry, err := r.Connect(account).Stat(ctx, path)
	if err != nil {
		if refused := remoteRefusal(err); refused != nil {
			r.Logger.LogAttrs(ctx, slog.LevelWarn, "an imported Nextcloud file could not be looked up",
				slog.String("request_id", a.requestID), slog.String("error", err.Error()))
			a.refuse(refused)
			return
		}
		a.fail("the file could not be looked up", err)
		return
	}
	if document.State == "ready" && entry.ETag != "" && entry.ETag == document.ETag {
		a.send(struct {
			Status string `json:"status"`
		}{"unchanged"})
		return
	}
	set, err := r.Rows.QueueRefresh(ctx, documentID, caller.UserID, connectorID, entry)
	if err != nil {
		a.fail("the refresh could not be recorded", err)
		return
	}
	if set {
		// The stored original is named after the file in Nextcloud, not
		// the document's title: a title can be edited, and the name's
		// extension is what the indexer matches a parser against.
		r.Downloads.Start([]Job{{
			DocumentID: documentID, UserID: caller.UserID, Account: account,
			Path: path, Name: entry.Name, ContentType: entry.ContentType,
		}}, produce.LogContext{RequestID: a.requestID, UserID: caller.UserID})
	}
	// Not set means another refresh got there between the read and the
	// write; either way a job is on its way.
	a.send(queued)
}
