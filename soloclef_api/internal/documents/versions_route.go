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

// VersionHistory reads what the two version reads read.
type VersionHistory interface {
	// Owns reports whether the account has the document.
	Owns(ctx context.Context, documentID int64, userID string) (bool, error)
	Versions(ctx context.Context, documentID int64) ([]VersionSummary, error)
	Version(ctx context.Context, documentID, number int64) (*Version, error)
}

// VersionsRoute serves GET /documents/{document_id}/versions
// (list_document_versions): a document's history, newest first, without the
// text of any version.
type VersionsRoute struct {
	Versions VersionHistory
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (r VersionsRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-document-versions",
		Method:      http.MethodGet,
		Path:        VersionsPath,
		Summary:     "Every version of a document",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/documents/{document_id}/versions, byte for byte.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct {
		DocumentID string `path:"document_id" doc:"The document's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r VersionsRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "a document's versions could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.uncaught(noCaller, nil)
		return
	}

	documentText := ctx.Param("document_id")

	invalid := make([]errorenvelope.Failure, 0, 1)
	document, _ := readNumber(documentText, &invalid, "path", "document_id")
	if len(invalid) > 0 {
		r.Errors.WriteValidation(response, requestID, invalid)
		return
	}

	// The document is the first statement and it carries the id, so an id
	// too wide to send fails here, before any refusal. Nothing in this route
	// catches, so every failure from here on is the 500 without CORS headers
	// (failure.go).
	if document.tooWide() {
		failed.uncaught(documentTooWide, errTooWide)
		return
	}
	found, err := r.Versions.Owns(ctx.Context(), document.value, caller.UserID)
	if err != nil {
		failed.uncaught(documentUnread, err)
		return
	}
	if !found {
		r.Errors.Write(response, http.StatusNotFound, documentAbsent, requestID)
		return
	}

	versions, err := r.Versions.Versions(ctx.Context(), document.value)
	if err != nil {
		failed.uncaught("could not read the document's versions", err)
		return
	}
	body, err := pythonjson.Marshal(buildVersionList(versions))
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
