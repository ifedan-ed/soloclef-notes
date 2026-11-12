package folders

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// DocumentMoveRoute serves `PUT /api/v1/documents/{document_id}/move`: one
// document into one folder, or out of every folder.
//
// It lives with the folders because a folder's contents are what it
// changes. Both the document and the folder must be the caller's.
type DocumentMoveRoute struct {
	Folders Rows
	// Arrivals is told when a ready document arrives in a folder; nil
	// announces nothing (arrival.go).
	Arrivals Arrivals
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// Now is the clock an arrival is timed by.
	Now func() time.Time
}

func (r DocumentMoveRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "move-document",
		Method:      http.MethodPut,
		Path:        DocumentMovePath,
		Summary:     "Move a document into a folder",
		Description: "Files a document of the caller's in one of their folders, " +
			"or in none when `folder_id` is null.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		DocumentID string `path:"document_id" doc:"The document's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DocumentMoveRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the document could not be moved"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		fail.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}
	requested, validation, decodeErr := ReadDocumentMoveRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		fail.fail(ctx, response, requestID, "a body reached the route that should not have", decodeErr)
		return
	}
	documentID, pathFailure, rangeErr := requestnumber.Parse(ctx.Param("document_id"), "path", "document_id")
	if !reportInvalid(r.Errors, response, requestID, pathFailure, validation) {
		return
	}
	if rangeErr != nil {
		// A number no id column can hold names no document.
		r.Errors.Write(response, http.StatusNotFound, DocumentNotFound, requestID)
		return
	}

	documents, err := r.Folders.Documents(ctx.Context(), caller.UserID, []int64{documentID})
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the document", err)
		return
	}
	if len(documents) == 0 {
		r.Errors.Write(response, http.StatusNotFound, DocumentNotFound, requestID)
		return
	}
	if !fileDocuments(ctx, response, requestID, fail, filing{
		Rows: r.Folders, Arrivals: r.Arrivals, Errors: r.Errors, Logger: r.Logger, At: nowOr(r.Now),
	}, caller.UserID, documents, requested.FolderID) {
		return
	}
	sendMessage(ctx, response, requestID, fail, DocumentMoved)
}

// BulkMoveRoute serves `PUT /api/v1/documents/bulk-move`: a selection
// dragged into a folder.
//
// The documents moved are the ones among the ids that are the caller's; an
// id that is another account's, or nobody's, is passed over without a word,
// so the answer cannot be used to ask which ids exist. The answer counts
// what was found, which is what was filed.
type BulkMoveRoute struct {
	Folders  Rows
	Arrivals Arrivals
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	Now      func() time.Time
}

func (r BulkMoveRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "bulk-move-documents",
		Method:      http.MethodPut,
		Path:        BulkMovePath,
		Summary:     "Move several documents into a folder",
		Description: "Files the caller's documents among `document_ids` in one of their folders, " +
			"or in none when `folder_id` is null.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r BulkMoveRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the documents could not be moved"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		fail.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}
	requested, validation, decodeErr := ReadBulkMoveRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		fail.fail(ctx, response, requestID, "a body reached the route that should not have", decodeErr)
		return
	}
	if !reportInvalid(r.Errors, response, requestID, nil, validation) {
		return
	}
	if len(requested.DocumentIDs) == 0 {
		r.Errors.Write(response, http.StatusBadRequest, NoDocumentIDs, requestID)
		return
	}

	documents, err := r.Folders.Documents(ctx.Context(), caller.UserID, requested.DocumentIDs)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the documents", err)
		return
	}
	if len(documents) == 0 {
		r.Errors.Write(response, http.StatusNotFound, NoDocumentsFound, requestID)
		return
	}
	if !fileDocuments(ctx, response, requestID, fail, filing{
		Rows: r.Folders, Arrivals: r.Arrivals, Errors: r.Errors, Logger: r.Logger, At: nowOr(r.Now),
	}, caller.UserID, documents, requested.FolderID) {
		return
	}
	sendMessage(ctx, response, requestID, fail, strconv.Itoa(len(documents))+" documents moved successfully")
}

// filing is what both moves need to file documents.
type filing struct {
	Rows     Rows
	Arrivals Arrivals
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	At       time.Time
}

// fileDocuments is the half the two moves share: check the target folder
// is the caller's, write the documents whose folder changes, and announce
// the arrivals. It reports false when it has already answered the request.
func fileDocuments(
	ctx huma.Context,
	response http.ResponseWriter,
	requestID string,
	fail failures,
	with filing,
	userID string,
	documents []Document,
	target requestbody.OptionalNumber,
) bool {
	if target.OutOfRange {
		with.Errors.Write(response, http.StatusNotFound, TargetFolderNotFound, requestID)
		return false
	}
	folderID := target.Value
	if folderID != nil {
		folder, err := with.Rows.Folder(ctx.Context(), userID, *folderID)
		if err != nil {
			fail.fail(ctx, response, requestID, "could not read the target folder", err)
			return false
		}
		if folder == nil {
			with.Errors.Write(response, http.StatusNotFound, TargetFolderNotFound, requestID)
			return false
		}
	}

	// Decided before the write, from the rows as read: afterwards every
	// document is in the folder and nothing says which ones arrived.
	announced := arrivals(userID, documents, folderID)
	if changed := moved(documents, folderID); len(changed) > 0 {
		if err := with.Rows.MoveDocuments(ctx.Context(), userID, changed, folderID); err != nil {
			fail.fail(ctx, response, requestID, "could not move the documents", err)
			return false
		}
	}
	announce(ctx.Context(), with.Arrivals, with.Logger, announced, with.At, requestID)
	return true
}
