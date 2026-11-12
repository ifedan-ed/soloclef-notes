package documents

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// failures writes this slice's 500s, and there are two kinds of them.
//
// Starlette has two error handlers and only one is inside its CORS
// middleware (internal/corsmirror; custom/docs/go-api-tier.md, divergence 5).
// Which one answers is decided by whether the Python route catches:
//
//   - get_document_by_chunk_id and get_document_chunks_paginated wrap their
//     whole body in `except Exception` and raise an HTTPException(500). That
//     is answered by ExceptionMiddleware, inside the CORS middleware, so the
//     500 carries the CORS headers. `caught` writes it.
//     get_documents_status, get_document_type_counts and
//     search_document_titles do the same.
//   - list_document_versions, get_document_version,
//     restore_document_version and get_watched_folders have no try at all. Whatever goes wrong in
//     them is answered by ServerErrorMiddleware, the outermost middleware, so
//     the 500 carries none. `uncaught` writes it.
//
// The body is the same sanitised envelope either way: app.py never lets a
// 500's own reason out.
//
// A route that catches still has one `uncaught` failure: rendering. The
// response model is serialised after the route has returned, so nothing the
// route wrapped in a try can catch it.
type failures struct {
	ctx       huma.Context
	errors    errorenvelope.Writer
	logger    *slog.Logger
	response  http.ResponseWriter
	requestID string
	// what is the sentence the log line opens with, naming the route.
	what string
}

func (f failures) caught(reason string, cause error) {
	f.log(reason, cause)
	f.errors.Write(f.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, f.requestID)
}

func (f failures) uncaught(reason string, cause error) {
	f.log(reason, cause)
	corsmirror.Suppress(f.ctx.Context())
	f.errors.Write(f.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, f.requestID)
}

func (f failures) log(reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", f.requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	f.logger.LogAttrs(f.ctx.Context(), slog.LevelError, f.what, attributes...)
}

// The reasons a failure is logged with, named once because several routes
// give them.
const (
	noCaller         = "the route ran without an authenticated caller"
	documentTooWide  = "the document id is wider than any statement can carry"
	chunkTooWide     = "the chunk id is wider than any statement can carry"
	versionTooWide   = "the version number is wider than any statement can carry"
	offsetTooWide    = "the page offset is wider than any statement can carry"
	documentUnread   = "could not read the document"
	answerUnrendered = "could not render the answer"
)
