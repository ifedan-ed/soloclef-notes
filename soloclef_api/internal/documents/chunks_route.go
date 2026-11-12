package documents

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// PagedChunks reads what the chunk page reads.
type PagedChunks interface {
	// Owns reports whether the account has the document.
	Owns(ctx context.Context, documentID int64, userID string) (bool, error)
	ChunkCount(ctx context.Context, documentID int64, userID string) (int64, error)
	ChunkPage(ctx context.Context, documentID int64, userID string, limit, offset int64) ([]Passage, error)
}

// ChunksRoute serves GET /documents/{document_id}/chunks
// (get_document_chunks_paginated): a document's chunks, a page at a time, in
// reading order.
type ChunksRoute struct {
	Chunks PagedChunks
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register adds the operation. No parameter is declared to Huma as anything
// but a string, and the three query parameters not at all: every refusal has
// to be the Python tier's 422 envelope, word for word.
func (r ChunksRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-document-chunks",
		Method:      http.MethodGet,
		Path:        ChunksPath,
		Summary:     "A page of a document's chunks",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/documents/{document_id}/chunks, byte for byte.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct {
		DocumentID string `path:"document_id" doc:"The document's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ChunksRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "a page of a document's chunks could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.uncaught(noCaller, nil)
		return
	}

	requested := ctx.URL()
	query := requested.Query()
	documentText := ctx.Param("document_id")

	// The signature's order: the path's document_id, then page, page_size and
	// start_offset. Every bad one is reported at once.
	invalid := make([]errorenvelope.Failure, 0, 4)
	document, _ := readNumber(documentText, &invalid, "path", "document_id")
	paging := chunkPaging{page: number{value: defaultPage}, pageSize: defaultPageSize}
	if read, named := readBounded(query, "page", 0, nil, &invalid); named {
		paging.page = read
	}
	largest := int64(largestPageSize)
	if read, named := readBounded(query, "page_size", smallestPageSize, &largest, &invalid); named {
		// Inside the bounds when there is no failure, so it fits.
		paging.pageSize = read.value
	}
	if read, named := readBounded(query, "start_offset", 0, nil, &invalid); named {
		paging.startOffset = &read
	}
	if len(invalid) > 0 {
		r.Errors.WriteValidation(response, requestID, invalid)
		return
	}

	// The document is the first statement and it carries the id.
	if document.tooWide() {
		failed.caught(documentTooWide, errTooWide)
		return
	}
	found, err := r.Chunks.Owns(ctx.Context(), document.value, caller.UserID)
	if err != nil {
		failed.caught(documentUnread, err)
		return
	}
	if !found {
		r.Errors.Write(response, http.StatusNotFound, documentAbsent, requestID)
		return
	}

	// The count before the page, and before the offset is found to be
	// unusable: a page of twenty digits is a 404 for a document the caller
	// does not have, and a 500 only for its owner.
	total, err := r.Chunks.ChunkCount(ctx.Context(), document.value, caller.UserID)
	if err != nil {
		failed.caught("could not count the document's chunks", err)
		return
	}
	offset, representable := paging.offset()
	if !representable {
		failed.caught(offsetTooWide, errTooWide)
		return
	}
	passages, err := r.Chunks.ChunkPage(ctx.Context(), document.value, caller.UserID, paging.pageSize, offset)
	if err != nil {
		failed.caught("could not read the page of chunks", err)
		return
	}

	body, err := pythonjson.Marshal(buildChunkPage(passages, total,
		paging.reportedPage(offset), paging.pageSize, hasMore(offset, len(passages), total)))
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
