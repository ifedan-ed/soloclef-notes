// Package documentcontent serves the text of one of the caller's documents,
// as the document viewer and the note editor open it:
//
//	GET /api/v1/documents/{document_id}/editor-content
//
// # What it does better than the Python
//
//   - **A read writes nothing.** The Python route converted an old editor
//     format and stored the result, or stitched the chunks back together,
//     inside a GET. Here a document's Markdown is a column it always has
//     (`documents.content`, which the chunks are cut from), and the person's
//     own edited text (`source_markdown`) wins when there is one.
//   - **One statement**, which names the owner and counts the chunks.
//   - **A document still being read is said to be**, with the 409 the viewer
//     waits on, rather than an empty page; a document that failed says why.
package documentcontent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Path is the route.
const Path = "/api/v1/documents/{document_id}/editor-content"

// The rich editor slows past these; a larger document opens in the code
// viewer instead, which the answer's viewer_mode says.
const (
	richEditorMaxBytes = 1 << 20
	richEditorMaxLines = 5000
)

// Route serves the read.
type Route struct {
	Database DBTX
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

type documentPath struct {
	DocumentID string `path:"document_id" doc:"The document's id"`
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-document-content",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "A document's text, as the viewer and the editor open it",
		Tags:        []string{"documents"},
	}, func(_ context.Context, _ *documentPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// answer is what the viewer reads.
type answer struct {
	DocumentID     int64      `json:"document_id"`
	Title          string     `json:"title"`
	DocumentType   string     `json:"document_type"`
	SourceMarkdown string     `json:"source_markdown"`
	ViewerMode     string     `json:"viewer_mode"`
	ChunkCount     int64      `json:"chunk_count"`
	UpdatedAt      *time.Time `json:"updated_at"`
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	refuse := func(status int, message string) {
		r.Errors.Write(response, status, message, requestID)
	}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		r.Logger.ErrorContext(ctx.Context(), "the document text route ran without a caller")
		refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
		return
	}
	id, err := strconv.ParseInt(ctx.Param("document_id"), 10, 64)
	if err != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"path", "document_id"}, Message: "Input should be a valid integer",
		}})
		return
	}
	row, err := New(r.Database).DocumentText(ctx.Context(), DocumentTextParams{ID: id, UserID: caller.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		refuse(http.StatusNotFound, "Document not found")
		return
	}
	if err != nil {
		r.Logger.ErrorContext(ctx.Context(), "could not read a document's text",
			slog.String("request_id", requestID), slog.String("error", err.Error()))
		refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
		return
	}

	text := row.Content
	if row.SourceMarkdown != nil {
		text = *row.SourceMarkdown
	}
	// A note may be empty; any other document with no text is one whose
	// reading has not finished, or failed.
	if strings.TrimSpace(text) == "" && row.DocumentType != "NOTE" {
		if status, message := unreadable(row.Status); status != 0 {
			refuse(status, message)
			return
		}
	}

	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(answer{
		DocumentID:     row.ID,
		Title:          row.Title,
		DocumentType:   row.DocumentType,
		SourceMarkdown: text,
		ViewerMode:     viewerMode(text),
		ChunkCount:     row.ChunkCount,
		UpdatedAt:      row.UpdatedAt,
	})
}

// viewerMode is "plate" (the rich editor) for a document it handles well,
// and "monaco" (the code viewer) for one too large for it.
func viewerMode(text string) string {
	if len(text) > richEditorMaxBytes || strings.Count(text, "\n")+1 > richEditorMaxLines {
		return "monaco"
	}
	return "plate"
}

// unreadable is the refusal for a document with no text, by its status:
// still being read (409, which the viewer retries), failed (422, with the
// reason), or ready and empty (400). A status that cannot be read is
// treated as ready, which is what the column's default says.
func unreadable(raw []byte) (int, string) {
	var status struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &status)
	switch status.State {
	case "pending", "processing":
		return http.StatusConflict, "This document is still being processed. Please wait a moment and try again."
	case "failed":
		reason := status.Reason
		if reason == "" {
			reason = "the reason was not recorded"
		}
		return http.StatusUnprocessableEntity, "Processing failed: " + reason + ". You can delete this document and upload it again."
	}
	return http.StatusBadRequest, "This document has no content. Try deleting it and uploading it again."
}
