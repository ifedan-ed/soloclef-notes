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

// VersionRoute serves GET /documents/{document_id}/versions/{version_number}
// (get_document_version): one version, with its text.
type VersionRoute struct {
	Versions VersionHistory
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (r VersionRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-document-version",
		Method:      http.MethodGet,
		Path:        VersionPath,
		Summary:     "One version of a document",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/documents/{document_id}/versions/{version_number}, " +
			"byte for byte.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct {
		DocumentID    string `path:"document_id" doc:"The document's id"`
		VersionNumber string `path:"version_number" doc:"The version's number"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r VersionRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "a version of a document could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.uncaught(noCaller, nil)
		return
	}

	// Both path parameters, in the path's order, reported together.
	invalid := make([]errorenvelope.Failure, 0, 2)
	document, _ := readNumber(ctx.Param("document_id"), &invalid, "path", "document_id")
	version, _ := readNumber(ctx.Param("version_number"), &invalid, "path", "version_number")
	if len(invalid) > 0 {
		r.Errors.WriteValidation(response, requestID, invalid)
		return
	}

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

	// The version number reaches no statement until the document is known
	// to be the caller's, so anyone else asking for a twenty-digit version
	// is told 404 and its owner is told 500.
	if version.tooWide() {
		failed.uncaught(versionTooWide, errTooWide)
		return
	}
	stored, err := r.Versions.Version(ctx.Context(), document.value, version.value)
	if err != nil {
		failed.uncaught("could not read the version", err)
		return
	}
	if stored == nil {
		r.Errors.Write(response, http.StatusNotFound, versionAbsent, requestID)
		return
	}

	body, err := pythonjson.Marshal(buildVersion(*stored))
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
