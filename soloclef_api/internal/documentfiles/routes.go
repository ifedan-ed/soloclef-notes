package documentfiles

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/artifacts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_filestore/objecthttp"
)

// The cache rules.
const (
	// A download is the current file, fetched because somebody pressed a
	// button; nothing should answer it from a cache.
	noStore = "private, no-store"
	// A content URL names one `document_files` row, and a row's bytes never
	// change: a re-upload is a new row with a new id. `private` because the
	// answer depended on who asked.
	immutable = "private, max-age=31536000, immutable"
)

// Routes is the three routes and what they share.
type Routes struct {
	Pool *pgxpool.Pool
	// Files is every store this process reads; a file's row says which one
	// holds it.
	Files *filestore.Stores
	// StallTimeout is how long a client may accept nothing before a stream
	// is dropped. However long a download takes otherwise is the client's
	// business. Required: there is no figure to guess that would not
	// silently disagree with the server's own write timeout.
	StallTimeout time.Duration
	Errors       errorenvelope.Writer
	Logger       *slog.Logger
}

type (
	documentPath struct {
		DocumentID string `path:"document_id"`
	}
	filePath struct {
		DocumentID string `path:"document_id"`
		FileID     string `path:"file_id"`
	}
)

// Register mounts the routes. It panics on a deployment that did not say
// how long a stalled client may be kept, at start-up and not on the first
// download.
func (r Routes) Register(api huma.API) {
	if r.StallTimeout <= 0 {
		panic("documentfiles: Routes needs a StallTimeout greater than zero")
	}
	if r.Files == nil {
		panic("documentfiles: Routes needs the file stores")
	}
	mount[documentPath](api, r, ManifestPath, "read-document-view-manifest", "How a document should open", r.manifest)
	mount[documentPath](api, r, DownloadPath, "download-document-original", "A document's uploaded file", r.download)
	mount[filePath](api, r, ContentPath, "read-document-file-content", "The bytes of one of a document's files", r.content)
}

// exchange is one request being answered.
type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

func mount[P any](api huma.API, r Routes, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: http.MethodGet, Path: path, Summary: summary, Tags: []string{"documents"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
			caller, ok := principal.FromContext(ctx.Context())
			if !ok {
				x.fail("the route ran without a caller", nil)
				return
			}
			x.userID = caller.UserID
			serve(x)
		}}, nil
	})
}

// ids reads the named path parameters as whole numbers, refusing every one
// that is not in a single 422. One too wide for a bigint names no row: it
// reads as 0, which no row has, and the route answers its 404.
func (x *exchange) ids(names ...string) ([]int64, bool) {
	values := make([]int64, 0, len(names))
	var failures []errorenvelope.Failure
	for _, name := range names {
		text := x.ctx.Param(name)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			value = 0
			if !digits(text) {
				failures = append(failures, errorenvelope.Failure{
					Location: []string{"path", name}, Message: "Input should be a valid integer"})
			}
		}
		values = append(values, value)
	}
	if len(failures) > 0 {
		x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
		return nil, false
	}
	return values, true
}

func digits(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

// fail logs the cause and answers the 500. A cancelled request is not a
// fault and is not logged as one.
func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a document file route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

func (r Routes) manifest(x *exchange) {
	ids, ok := x.ids("document_id")
	if !ok {
		return
	}
	row, err := New(r.Pool).ManifestOf(x.ctx.Context(), ManifestOfParams{DocumentID: ids[0], UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, documentAbsent)
		return
	}
	if err != nil {
		x.fail("could not read the document's manifest", err)
		return
	}
	x.response.Header().Set("Content-Type", "application/json")
	// The answer changes when an import finishes; the app polls it.
	x.response.Header().Set("Cache-Control", noStore)
	x.response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(x.response).Encode(describe(row))
}

// stored is one file row as the two byte routes send it.
type stored struct {
	backend, key, filename string
	mimeType, checksum     *string
}

// download sends the document's newest original as an attachment.
func (r Routes) download(x *exchange) {
	ids, ok := x.ids("document_id")
	if !ok {
		return
	}
	ctx := x.ctx.Context()
	queries := New(r.Pool)
	row, err := queries.OriginalOf(ctx, OriginalOfParams{DocumentID: ids[0], UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		// Which of the two it is: a document that is not the caller's is
		// "no such document", exactly as an id never issued is.
		owned, err := queries.DocumentIsOwned(ctx, DocumentIsOwnedParams{DocumentID: ids[0], UserID: x.userID})
		switch {
		case err != nil:
			x.fail("could not look for the document", err)
		case owned:
			x.refuse(http.StatusNotFound, originalAbsent)
		default:
			x.refuse(http.StatusNotFound, documentAbsent)
		}
		return
	}
	if err != nil {
		x.fail("could not read the document's original file", err)
		return
	}
	// Always an attachment, whatever the type: this route's purpose is
	// "save this".
	r.send(x, stored{row.StorageBackend, row.StorageKey, row.OriginalFilename, row.MimeType, row.ChecksumSha256},
		originalAbsent, noStore, false)
}

// content sends one stored file to a viewer.
func (r Routes) content(x *exchange) {
	ids, ok := x.ids("document_id", "file_id")
	if !ok {
		return
	}
	row, err := New(r.Pool).FileOf(x.ctx.Context(), FileOfParams{FileID: ids[1], DocumentID: ids[0], UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, fileAbsent)
		return
	}
	if err != nil {
		x.fail("could not read the document's file", err)
		return
	}
	r.send(x, stored{row.StorageBackend, row.StorageKey, row.OriginalFilename, row.MimeType, row.ChecksumSha256},
		fileAbsent, immutable, true)
}

// send streams the file. `viewed` says the caller is a viewer: the type is
// then the viewer's (a PDF stored as bytes is sent as a PDF), and it may be
// shown in the page if it is one of the few types that are safe to show.
func (r Routes) send(x *exchange, file stored, absent, cacheControl string, viewed bool) {
	storedType := ""
	if file.mimeType != nil {
		storedType = *file.mimeType
	}
	mediaType := typeOf(storedType)
	inline := false
	if viewed {
		if canonical := viewedType(file.filename, storedType); canonical != "" {
			mediaType = canonical
		}
		// An uploaded file is somebody's content; showing one in the page
		// is running it with this site's cookies. Only the types whose
		// built-in viewers execute nothing are shown (artifacts.ShownInline).
		inline = artifacts.ShownInline(mediaType)
	}
	// The row's checksum is the validator when it has one; without one
	// objecthttp derives a tag from the stored object.
	tag := ""
	if file.checksum != nil && *file.checksum != "" {
		tag = `"` + *file.checksum + `"`
	}
	artifacts.Gate{Errors: r.Errors, Logger: r.Logger}.StreamStored(x.ctx, x.response, x.requestID, r.Files, r.StallTimeout,
		artifacts.StoredObject{
			Backend: file.backend, Key: file.key, Absent: absent,
			Headers: objecthttp.Headers{
				ContentType:  mediaType,
				ETag:         tag,
				CacheControl: cacheControl,
				Disposition:  artifacts.ContentDisposition(file.filename, inline),
			},
		})
}
