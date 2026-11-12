// Package documentreindex serves POST /api/v1/documents/{document_id}/reindex:
// hand a document back to the indexing worker.
//
// It is what a person uses after a failure (the parser was down, the job
// queue did not answer when the file was uploaded) and what switches a
// document to a different parser: `?parser=datalab` records the choice on
// the row and the job reads it from there.
//
// The job is the same message an upload queues (internal/indexqueue). The
// worker parses the stored original again, keeps every chunk whose text did
// not change, and swaps the rest in one transaction, so the document stays
// searchable as it was until it is searchable as it will be.
//
// A document is re-indexed only for the account that owns it. Both
// statements name the owner, so another account's document is answered as
// an id that names nothing.
package documentreindex

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Path is the route.
const Path = "/api/v1/documents/{document_id}/reindex"

// What the route says.
const (
	Queued        = "Document queued for indexing"
	AlreadyQueued = "This document is already being indexed."
	BeingDeleted  = "This document is being deleted."
	NothingToRead = "This document has no uploaded file and no text, so there is nothing to index."
	NoFileToParse = "This document has no uploaded file, so a parser does not apply to it. Re-index it without naming one."
	NotQueued     = "The document could not be queued for indexing right now. Please try again."
)

// Subject is the document as a re-index decides from it.
type Subject struct {
	State string
	// Filename is the newest original's name, "" when the document has no
	// uploaded file.
	Filename string
	// HasText: the row holds Markdown the indexer can index without a file.
	HasText bool
}

// Rows is the database as the route uses it.
type Rows interface {
	// Subject is the account's document, or nil when the account has no
	// such document.
	Subject(ctx context.Context, documentID int64, userID string) (*Subject, error)
	// HandBack sets the document waiting, with the parser named when parser
	// is not "". False means its state changed since Subject read it and a
	// job is already on its way, or it is being deleted.
	HandBack(ctx context.Context, documentID int64, userID, parser string) (bool, error)
}

// PostgresRows is Rows over the pool. Each is one statement, so each is its
// own transaction.
type PostgresRows struct {
	Pool *pgxpool.Pool
}

func (p PostgresRows) Subject(ctx context.Context, documentID int64, userID string) (*Subject, error) {
	row, err := New(p.Pool).ReindexSubject(ctx, ReindexSubjectParams{ID: documentID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Subject{State: row.State, Filename: row.Filename, HasText: row.HasText}, nil
}

func (p PostgresRows) HandBack(ctx context.Context, documentID int64, userID, parser string) (bool, error) {
	arguments := HandBackToIndexerParams{ID: documentID, UserID: userID}
	if parser != "" {
		arguments.Parser = &parser
	}
	changed, err := New(p.Pool).HandBackToIndexer(ctx, arguments)
	return changed == 1, err
}

// Indexing queues the job (indexqueue.Indexing).
type Indexing interface {
	Enqueue(ctx context.Context, documentID int64, logContext produce.LogContext) error
}

// Route serves the re-index.
type Route struct {
	Rows     Rows
	Parsers  parserchoice.Registry
	Indexing Indexing
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "reindex-document",
		Method:      http.MethodPost,
		Path:        Path,
		Summary:     "Index a document again",
		Description: "Queues the document for the indexing worker. An optional `parser` query parameter " +
			"(local, datalab, llamaparse or unstructured) changes the parser that reads its file.",
		Tags: []string{"documents"},
	}, func(context.Context, *struct {
		DocumentID string `path:"document_id" doc:"The document's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// refusal is a status and a sentence.
type refusal struct {
	status  int
	message string
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, signedIn := principal.FromContext(ctx.Context())
	if !signedIn {
		// Unreachable behind the gate; a 500 if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}
	documentID, failure, rangeErr := requestnumber.Parse(ctx.Param("document_id"), "path", "document_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	if rangeErr != nil || documentID < 1 {
		r.Errors.Write(response, http.StatusNotFound, notFound(documentID), requestID)
		return
	}

	parser, refused, err := r.decide(ctx.Context(), caller, documentID, ctx.Query("parser"))
	if err != nil {
		r.fail(ctx, response, requestID, "could not decide whether the document may be indexed again", err)
		return
	}
	if refused != nil {
		r.Errors.Write(response, refused.status, refused.message, requestID)
		return
	}

	handed, err := r.Rows.HandBack(ctx.Context(), documentID, caller.UserID, parser)
	if err != nil {
		r.fail(ctx, response, requestID, "could not set the document waiting", err)
		return
	}
	if !handed {
		// Another request got there between the read and the write.
		r.Errors.Write(response, http.StatusConflict, AlreadyQueued, requestID)
		return
	}
	// After the write is committed: the worker reads the row, and the
	// parser on it, the moment it takes the message.
	if err := r.Indexing.Enqueue(ctx.Context(), documentID, produce.LogContext{
		RequestID: requestID, UserID: caller.UserID,
	}); err != nil {
		// The document has been marked failed with the reason
		// (indexqueue); it is not left waiting for a job that is not there.
		r.Errors.Write(response, http.StatusServiceUnavailable, NotQueued, requestID)
		return
	}

	body, err := json.Marshal(struct {
		Message    string `json:"message"`
		DocumentID int64  `json:"document_id"`
	}{Queued, documentID})
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	header := response.Header()
	header.Set("Content-Type", "application/json")
	header.Set(requestcontext.Header, requestID)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}

// decide is every refusal, and the parser to record ("" to keep the
// document's own).
func (r Route) decide(ctx context.Context, caller principal.Principal, documentID int64, submitted string) (string, *refusal, error) {
	subject, err := r.Rows.Subject(ctx, documentID, caller.UserID)
	if err != nil {
		return "", nil, err
	}
	if subject == nil {
		// No such document, or another account's: the same answer, so a
		// document id cannot be probed for.
		return "", &refusal{http.StatusNotFound, notFound(documentID)}, nil
	}

	switch subject.State {
	case "pending", "processing":
		return "", &refusal{http.StatusConflict, AlreadyQueued}, nil
	case "deleting":
		return "", &refusal{http.StatusConflict, BeingDeleted}, nil
	}
	if subject.Filename == "" && !subject.HasText {
		return "", &refusal{http.StatusConflict, NothingToRead}, nil
	}

	if submitted == "" {
		// The document keeps the parser it has.
		return "", nil, nil
	}
	if subject.Filename == "" {
		return "", &refusal{http.StatusBadRequest, NoFileToParse}, nil
	}
	chosen, refused := parserchoice.Choose(r.Parsers, submitted)
	if refused != nil {
		return "", &refusal{parserchoice.Status, refused.Message}, nil
	}
	unread, err := parserchoice.Reads(r.Parsers, chosen, subject.Filename)
	if err != nil {
		return "", nil, err
	}
	if unread != nil {
		return "", &refusal{parserchoice.Status, unread.Message}, nil
	}
	return chosen.String(), nil, nil
}

func notFound(documentID int64) string {
	return "Document with id " + strconv.FormatInt(documentID, 10) + " not found"
}

func (r Route) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "a document could not be queued for indexing", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
