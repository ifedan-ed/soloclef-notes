package drivesync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivefile"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
)

// Routes are the import and "sync now".
//
// cmd/api must not list the import in `withBodies`: it reads its own body,
// with its own limit.
type Routes struct {
	Rows    Rows
	Parsers parserchoice.Registry
	// Queue is where the job is published.
	Queue celeryqueue.Enqueuer
	// Configured says whether this deployment has a Google project
	// (driveconnect.Settings.Missing is empty). Without one nothing could
	// fetch a file, so both routes answer 503 and write nothing.
	Configured bool
	Errors     errorenvelope.Writer
	Logger     *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Rows == nil || r.Parsers == nil || r.Queue == nil || r.Logger == nil {
		return errors.New("drivesync: the rows, the parsers, the queue and the logger are all required")
	}
	return nil
}

func (r Routes) Register(api huma.API) {
	type connector struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
	}
	for _, route := range []struct {
		id, path, summary string
		serve             func(*asking)
	}{
		{"import-from-google-drive", ImportPath, "Import the files picked in the Google Picker", r.importPicked},
		{"sync-connector-now", IndexPath, "Bring a connection's imported files up to date now", r.syncNow},
	} {
		serve := route.serve
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: http.MethodPost, Path: route.path, Summary: route.summary, Tags: []string{"connectors"},
		}, func(context.Context, *connector) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				serve(&asking{routes: r, ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())})
			}}, nil
		})
	}
}

// asking is one request being answered.
type asking struct {
	routes    Routes
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
}

func (a *asking) refuse(status int, message string) {
	a.routes.Errors.Write(a.response, status, message, a.requestID)
}

func (a *asking) fail(reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", a.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	a.routes.Logger.LogAttrs(a.ctx.Context(), slog.LevelError, "a Google Drive sync route failed", attributes...)
	a.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

func (a *asking) send(answer any) {
	body, err := json.Marshal(answer)
	if err != nil {
		a.fail("the answer could not be encoded", err)
		return
	}
	pythonjson.Send(a.response, http.StatusOK, body)
}

// connection is what both routes begin with: the caller, and their own
// connection by the path's id. absent is the 404's words. ok is false when
// the answer has been written.
func (a *asking) connection(absent string) (caller principal.Principal, held *Connection, ok bool) {
	caller, signedIn := principal.FromContext(a.ctx.Context())
	if !signedIn {
		// Unreachable behind the gate; a 500 if the wiring is ever wrong.
		a.fail("the route ran without an authenticated caller", nil)
		return caller, nil, false
	}
	id, failure, rangeErr := requestnumber.Parse(a.ctx.Param("connector_id"), "path", "connector_id")
	if failure != nil {
		a.routes.Errors.WriteValidation(a.response, a.requestID, []errorenvelope.Failure{*failure})
		return caller, nil, false
	}
	if rangeErr != nil || id < 1 {
		a.refuse(http.StatusNotFound, absent)
		return caller, nil, false
	}
	held, err := a.routes.Rows.Connection(a.ctx.Context(), caller.UserID, id)
	if err != nil {
		a.fail("the connection could not be read", err)
		return caller, nil, false
	}
	if held == nil {
		a.refuse(http.StatusNotFound, absent)
		return caller, nil, false
	}
	return caller, held, true
}

// usable answers for a Drive connection nothing can be fetched with: a
// deployment with no Google project, or a grant Google has refused. It
// reports whether the connection can be used.
func (a *asking) usable(held *Connection) bool {
	if !a.routes.Configured {
		a.routes.Errors.WriteCoded(a.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, a.requestID)
		return false
	}
	if drivegrant.Read(held.Config).AuthExpired {
		// The code the web offers "connect again" for. A 400, never a
		// 401: a 401 from this API signs the person out of the app, and
		// it is Google that refused, not this server.
		a.routes.Errors.WriteCoded(a.response, http.StatusBadRequest, reconnectCode, reconnect, a.requestID)
		return false
	}
	return true
}

// publishTimeout bounds publishing the job and, if that fails, telling the
// documents. It runs outside the request's context: the import is
// committed, and a browser that gave up must not leave its documents
// waiting for a job that was never sent.
const publishTimeout = 15 * time.Second

// publish queues the sync of a connection.
func (a *asking) publish(ctx context.Context, request drivesyncjob.Request) error {
	_, err := a.routes.Queue.Enqueue(ctx, produce.Message{
		Task: drivesyncjob.Task, Kwargs: request.Kwargs(),
		// Keyword arguments only: the (empty) positional arguments are
		// written as a tuple, as Celery's send_task writes them.
		ArgsAsTuple: true,
		Context:     produce.LogContext{RequestID: a.requestID, UserID: request.UserID},
		// The outcome is the documents' own statuses; nobody reads a result.
		IgnoreResult: true,
	})
	return err
}

func (r Routes) importPicked(a *asking) {
	caller, held, ok := a.connection(connectionAbsent)
	if !ok {
		return
	}
	r.importInto(a, caller.UserID, held, a.ctx.BodyReader())
}

// ImportFor is the import for an account that has no session in this
// request: the phone app's Picker page (internal/apphandoff), whose ticket
// already says whose connection it is and which notebook the files go to.
// It answers exactly as the route does, from the same checks, so a pick
// from the app and one from the website become the same documents. body is
// the route's own body ({files, notebook_id}).
func (r Routes) ImportFor(ctx huma.Context, userID string, connectorID int64, body io.Reader) {
	a := &asking{routes: r, ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	held, err := r.Rows.Connection(ctx.Context(), userID, connectorID)
	if err != nil {
		a.fail("the connection could not be read", err)
		return
	}
	if held == nil {
		a.refuse(http.StatusNotFound, connectionAbsent)
		return
	}
	r.importInto(a, userID, held, body)
}

// importInto imports the picks in body into userID's connection held.
func (r Routes) importInto(a *asking, userID string, held *Connection, raw io.Reader) {
	// Another kind of connection is not a Drive connection, whoever's.
	if held.Kind != driveKind {
		a.refuse(http.StatusNotFound, connectionAbsent)
		return
	}
	body, wrong := readRequest(raw)
	if wrong != "" {
		a.refuse(http.StatusUnprocessableEntity, wrong)
		return
	}
	if !a.usable(held) {
		return
	}
	// The connection's parser, or the deployment's default when it names
	// none. One that is no longer configured is refused by name: files
	// would be stored that nothing could read.
	chosen, unconfigured := parserchoice.Choose(r.Parsers, held.Parser)
	if unconfigured != nil {
		a.refuse(parserchoice.Status, unconfigured.Message)
		return
	}
	ctx := a.ctx.Context()
	if body.NotebookID != nil {
		owned, err := r.Rows.OwnsNotebook(ctx, userID, *body.NotebookID)
		if err != nil {
			a.fail("the notebook could not be read", err)
			return
		}
		if !owned {
			// Another account's notebook and no notebook: one answer.
			a.refuse(http.StatusNotFound, notebookAbsent)
			return
		}
	}

	importable, unsupported := []Pick{}, []string{}
	for _, pick := range body.Picks {
		// Judged by the name the file will be stored under: a Google Doc
		// arrives as Word, and it is Word the parser must read.
		kept, storable := drivefile.StoredAs(pick.Name, pick.MimeType)
		if storable {
			unread, err := parserchoice.Reads(r.Parsers, chosen, kept.Name)
			if err != nil {
				a.fail("the parsers could not be asked about a file", err)
				return
			}
			storable = unread == nil
		}
		if storable {
			importable = append(importable, pick)
		} else {
			unsupported = append(unsupported, pick.Name)
		}
	}

	var started Started
	if len(importable) > 0 {
		var err error
		started, err = r.Rows.Start(ctx, Import{ConnectorID: held.ID, UserID: userID, NotebookID: body.NotebookID, Picks: importable})
		var deleting *errBeingDeleted
		switch {
		case errors.As(err, &deleting):
			a.refuse(http.StatusConflict, `"`+deleting.name+`" is being deleted. Import it again in a moment.`)
			return
		case errors.Is(err, errNotebookGone):
			a.refuse(http.StatusNotFound, notebookAbsent)
			return
		case err != nil:
			a.fail("the import could not be recorded", err)
			return
		}
		// After the commit: the worker reads the documents the moment it
		// takes the message.
		publishCtx, done := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
		defer done()
		fileIDs := make([]string, len(importable))
		for index, pick := range importable {
			fileIDs[index] = pick.ID
		}
		if err := a.publish(publishCtx, drivesyncjob.Request{ConnectorID: held.ID, UserID: userID, FileIDs: fileIDs}); err != nil {
			r.Logger.LogAttrs(ctx, slog.LevelError, "a Google Drive import's job could not be queued",
				slog.String("request_id", a.requestID), slog.String("error", err.Error()))
			if err := r.Rows.MarkNotQueued(publishCtx, userID, started.Waiting, QueueingFailed); err != nil {
				// Left "downloading"; the next sync of everything finds
				// them abandoned and fetches them.
				r.Logger.LogAttrs(ctx, slog.LevelError, "the documents of an import that was not queued could not be marked",
					slog.String("request_id", a.requestID), slog.String("error", err.Error()))
			}
			a.refuse(http.StatusServiceUnavailable, queueDown)
			return
		}
	}
	r.Logger.LogAttrs(ctx, slog.LevelInfo, "Google Drive import started",
		slog.String("request_id", a.requestID), slog.String("user_id", userID),
		slog.Int("queued", len(importable)), slog.Int("unsupported", len(unsupported)))
	documentIDs := started.DocumentIDs
	if documentIDs == nil {
		documentIDs = []int64{}
	}
	a.send(struct {
		DocumentIDs []int64  `json:"document_ids"`
		Queued      int      `json:"queued"`
		Unsupported []string `json:"unsupported"`
	}{documentIDs, len(importable), unsupported})
}

func (r Routes) syncNow(a *asking) {
	caller, held, ok := a.connection(connectorAbsent)
	if !ok {
		return
	}
	// The caller's own connection of another kind: it is there, and has
	// nothing this route can start.
	if held.Kind != driveKind {
		a.refuse(http.StatusBadRequest, nothingToSync)
		return
	}
	if !a.usable(held) {
		return
	}
	if !held.Enabled {
		a.refuse(http.StatusBadRequest, switchedOff)
		return
	}
	publishCtx, done := context.WithTimeout(context.WithoutCancel(a.ctx.Context()), publishTimeout)
	defer done()
	if err := a.publish(publishCtx, drivesyncjob.Request{ConnectorID: held.ID, UserID: caller.UserID}); err != nil {
		r.Logger.LogAttrs(a.ctx.Context(), slog.LevelError, "a Google Drive sync could not be queued",
			slog.String("request_id", a.requestID), slog.String("error", err.Error()))
		a.refuse(http.StatusServiceUnavailable, queueDown)
		return
	}
	a.send(struct {
		Message     string `json:"message"`
		ConnectorID int64  `json:"connector_id"`
	}{syncStarted, held.ID})
}
