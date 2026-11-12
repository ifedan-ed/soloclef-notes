package uploads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

// FolderRoute serves POST /api/v1/documents/folder-upload: a folder a
// person picked, or one the desktop app keeps in step, sent as its files
// and each file's path inside the folder.
//
// It is the upload route with a place for each file. The form's fields
// come first (folder_name, relative_paths, and optionally
// root_folder_id, use_vision_llm and parser), then the files, the n-th file
// belonging to the n-th path. Each file is streamed to the file store as it
// arrives; one transaction then finds or makes the folders the paths name
// and writes a pending document and a file row per file; and each
// document's index job is queued after the commit. From there a folder
// file is indexed exactly as an upload is, by the indexing worker, with the
// parser its row names.
//
// # Better than the route it replaces
//
//   - Any of the four parsers. The Python task parsed folder files itself
//     and refused the three hosted parsers; here the parser is a column on
//     the document and the worker routes by it, as for every other file.
//   - Streamed. The Python route read every file into memory and wrote it
//     to a temporary file on the API server's disk for a task on another
//     machine to open, which only worked while the two shared a disk. The
//     memory a request costs here is the copy buffer.
//   - The original is kept. The Python task indexed the text and stored no
//     file, so a folder file could not be downloaded or parsed again.
//   - Whole or not at all. A request's folders, documents and file rows are
//     one transaction (RecordFolder).
//   - Simultaneous requests agree on the folder. The web app sends a folder
//     as three requests at once, each asking for the same top folder;
//     internal/folderchain says how they share it.
//   - A file the parser does not read, or an empty one, is left out and
//     named in the answer (skipped_files). The Python task counted it as
//     failed in a log line nobody was shown. The rest of the folder is not
//     held up by it: a folder holds whatever it holds.
//   - A path is checked before anything is stored: no "..", no empty part,
//     no part a folder could not be called.
//
// cmd/api must NOT list it in `withBodies`, for the upload route's reason.
type FolderRoute struct {
	Rows FolderRows
	// Files is the store new files are written to.
	Files   filestore.Writer
	Parsers parserchoice.Registry
	// Vision decides whether the caller may switch use_vision_llm on.
	Vision   Vision
	Indexing Indexing
	// Limits are the upload route's: the same per-file size and the same
	// number of files a request.
	Limits parserchoice.Limits
	// Stall is how long the client may send nothing before the upload is
	// dropped, and how long the answer may take to be accepted.
	Stall  time.Duration
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register mounts the route.
func (r FolderRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "upload-folder",
		Method:      http.MethodPost,
		Path:        FolderPath,
		Summary:     "Upload a folder's files as documents, in the folders their paths name",
		Description: "multipart/form-data: folder_name and relative_paths (a JSON list, one path per file), " +
			"optionally root_folder_id, use_vision_llm and parser, then the files parts in the paths' order. " +
			"The fields must precede the files; the files are streamed to storage as they arrive.",
		Tags: []string{"documents"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// skippedFile is a file of the folder that was left out, and why.
type skippedFile struct {
	RelativePath string `json:"relative_path"`
	// Reason is "not_read_by_parser" or "empty".
	Reason string `json:"reason"`
	// Detail is the sentence for a person.
	Detail string `json:"detail"`
}

// folderAnswer is the response body. The first four fields are the ones the
// web and desktop apps read; the rest say what became of each file.
type folderAnswer struct {
	Message      string `json:"message"`
	Status       string `json:"status"`
	RootFolderID int64  `json:"root_folder_id"`
	// FileCount is the files the request carried.
	FileCount int `json:"file_count"`
	// DocumentIDs are the documents handed to the indexer: new files, and
	// files whose bytes changed or whose last indexing failed.
	DocumentIDs []int64 `json:"document_ids"`
	// UnchangedDocumentIDs are the documents that already hold these bytes.
	UnchangedDocumentIDs []int64       `json:"unchanged_document_ids"`
	SkippedFiles         []skippedFile `json:"skipped_files"`
}

func (r FolderRoute) serve(ctx huma.Context) {
	started := time.Now()
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	reply := replier{logger: r.Logger, errors: r.Errors, ctx: ctx.Context(), response: response, requestID: requestID}

	caller, signedIn := principal.FromContext(ctx.Context())
	if !signedIn {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// upload if the wiring is ever wrong.
		reply.fail("the route ran without an authenticated caller", nil)
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(ctx.Header("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		reply.refuse(refuse(http.StatusBadRequest, NotMultipart))
		return
	}
	body, err := pace(ctx.BodyReader(), response, r.Stall)
	if err != nil {
		reply.fail("the upload cannot be read at the client's pace", err)
		return
	}
	reply.body = body

	upload := &folderReceiving{route: r, caller: caller, body: body}
	refused, err := upload.receive(ctx.Context(), multipart.NewReader(body, parameters["boundary"]))
	if refused == nil && err == nil {
		refused = upload.complete()
	}
	if refused != nil || err != nil {
		// Whatever was stored before the request was refused is not owned
		// by any row and never will be.
		discard(ctx.Context(), r.Files, r.Logger, requestID, upload.keys())
		if err != nil {
			reply.fail("the folder upload could not be received", err)
			return
		}
		reply.refuse(refused)
		return
	}

	recorded, err := r.Rows.RecordFolder(ctx.Context(), FolderBatch{
		UserID: caller.UserID, Parser: upload.chosen.String(), Vision: upload.form.vision,
		FolderName: upload.form.folderName, RootFolderID: upload.form.rootFolderID, Files: upload.staged,
	})
	if err != nil {
		discard(ctx.Context(), r.Files, r.Logger, requestID, upload.keys())
		var deleting *ErrBeingDeleted
		var moved *ErrMovedMeanwhile
		switch {
		case errors.Is(err, ErrRootFolderGone):
			reply.refuse(refuse(http.StatusNotFound, RootFolderAbsent))
		case errors.As(err, &deleting):
			reply.refuse(refuse(http.StatusConflict, "%q is being deleted. Upload it again in a moment.", deleting.Name))
		case errors.As(err, &moved):
			reply.refuse(refuse(http.StatusConflict, "%q was changed by another upload while it was uploading. Nothing was saved; upload again.", moved.Name))
		default:
			reply.fail("the folder upload could not be recorded", err)
		}
		return
	}

	// From here the rows are committed. What is left cannot undo them: the
	// copies that were not kept are removed, and each document's job is
	// queued (a job that cannot be queued marks its document failed).
	result := folderAnswer{
		Message: fmt.Sprintf("Folder upload started for %d file(s)", upload.received), Status: FolderStarted,
		RootFolderID: recorded.RootFolderID, FileCount: upload.received,
		DocumentIDs: []int64{}, UnchangedDocumentIDs: []int64{}, SkippedFiles: upload.skipped,
	}
	if result.SkippedFiles == nil {
		result.SkippedFiles = []skippedFile{}
	}
	// Empty files were stored only to be counted; no row names them.
	unkept := append([]string{}, upload.emptied...)
	var bytes int64
	logContext := produce.LogContext{RequestID: requestID, UserID: caller.UserID}
	for index, file := range recorded.Files {
		bytes += upload.staged[index].Size
		if !file.Outcome.keepsFile() {
			unkept = append(unkept, upload.staged[index].Key)
		}
		if !file.Outcome.queues() {
			result.UnchangedDocumentIDs = append(result.UnchangedDocumentIDs, file.DocumentID)
			continue
		}
		result.DocumentIDs = append(result.DocumentIDs, file.DocumentID)
		// A failure is the document's now: it is marked failed with a
		// reason, and the answer lists it either way.
		_ = r.Indexing.Enqueue(ctx.Context(), file.DocumentID, logContext)
	}
	discard(ctx.Context(), r.Files, r.Logger, requestID, unkept)

	r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "folder files uploaded",
		slog.String("request_id", requestID), slog.String("user_id", caller.UserID),
		slog.Int64("root_folder_id", recorded.RootFolderID),
		slog.String("parser", upload.chosen.String()), slog.Int("files", upload.received),
		slog.Int("queued", len(result.DocumentIDs)), slog.Int("unchanged", len(result.UnchangedDocumentIDs)),
		slog.Int("skipped", len(result.SkippedFiles)),
		slog.Int64("bytes", bytes), slog.Duration("took", time.Since(started)))
	reply.send(result)
}

// folderReceiving is one folder request as it is read.
type folderReceiving struct {
	route  FolderRoute
	caller principal.Principal
	body   *pacedBody

	form folderFields
	// admitted is set once the fields have been checked: the moment the
	// first file part arrives.
	admitted bool
	chosen   selection.Selection
	// received counts the file parts, kept or not: the n-th belongs to the
	// n-th path.
	received int
	staged   []FolderFile
	skipped  []skippedFile
	// emptied are the keys of empty files, stored (the store is what
	// counts the bytes) and then left out; they are removed with the rest.
	emptied []string
}

// keys is every object this request put in the store.
func (u *folderReceiving) keys() []string {
	keys := append([]string{}, u.emptied...)
	for _, file := range u.staged {
		keys = append(keys, file.Key)
	}
	return keys
}

// receive reads the form in order: fields, then files, each file streamed
// to the store as it arrives.
func (u *folderReceiving) receive(ctx context.Context, form *multipart.Reader) (*refusal, error) {
	for {
		part, err := form.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			// A body that is not the multipart it claims to be, or a
			// client that stopped sending.
			return refuse(http.StatusBadRequest, Interrupted), nil
		}
		if part.FormName() != fieldFiles {
			if u.admitted {
				return refuse(http.StatusBadRequest, FieldAfterFile), nil
			}
			if refused := u.form.take(part); refused != nil {
				return refused, nil
			}
			continue
		}
		if !u.admitted {
			if refused, err := u.admit(ctx); refused != nil || err != nil {
				return refused, err
			}
			u.admitted = true
		}
		if refused, err := u.store(ctx, part); refused != nil || err != nil {
			return refused, err
		}
	}
}

// admit checks the fields, once, before the first byte of the first file
// is read.
func (u *folderReceiving) admit(ctx context.Context) (*refusal, error) {
	form := u.form
	if form.folderName == "" || !form.pathsSent {
		return refuse(http.StatusBadRequest, FolderFieldsFirst), nil
	}
	if len(form.paths) > u.route.Limits.MaxFiles {
		return refuse(http.StatusBadRequest, "A folder upload carries at most %d files; send the rest in another request", u.route.Limits.MaxFiles), nil
	}
	refusal, err := admitVision(ctx, u.route.Vision, form.vision, u.caller.UserID)
	if err != nil {
		return nil, err
	}
	if refusal != "" {
		// Refused, not ignored, as the upload route refuses it.
		return refuse(http.StatusBadRequest, "%s", refusal), nil
	}
	chosen, unknown := parserchoice.Choose(u.route.Parsers, form.parser)
	if unknown != nil {
		return refuse(parserchoice.Status, "%s", unknown.Message), nil
	}
	u.chosen = chosen

	// The folder must be the caller's own. Another account's folder is
	// answered exactly as one that was never made, so the route does not
	// say which folder ids are in use.
	if form.rootFolderID != nil {
		found, err := u.route.Rows.FolderIn(ctx, *form.rootFolderID, u.caller.UserID)
		if err != nil {
			return nil, err
		}
		if !found {
			return refuse(http.StatusNotFound, RootFolderAbsent), nil
		}
	}
	return nil, nil
}

// store takes one file part: the next path's file.
func (u *folderReceiving) store(ctx context.Context, part *multipart.Part) (*refusal, error) {
	if u.received >= len(u.form.paths) {
		return refuse(http.StatusBadRequest, "Mismatch: more files than the %d relative_paths", len(u.form.paths)), nil
	}
	path := u.form.paths[u.received]
	u.received++

	// A file the parser does not read is left out before a byte of it is
	// read: the multipart reader skips what is left of a part when the
	// next one is asked for.
	if !readAsPicture(u.form.vision, path.name) {
		unread, err := parserchoice.Reads(u.route.Parsers, u.chosen, path.name)
		if err != nil {
			return nil, err
		}
		if unread != nil {
			u.skipped = append(u.skipped, skippedFile{RelativePath: path.relative, Reason: skippedUnread, Detail: unread.Message})
			return nil, nil
		}
	}

	file, refused, err := stager{rows: u.route.Rows, files: u.route.Files, limits: u.route.Limits, logger: u.route.Logger}.put(
		ctx, u.body, part, path.name, part.Header.Get("Content-Type"),
		FolderIdentity(u.form.folderName, path.relative, u.caller.UserID), u.caller.UserID)
	if refused != nil || err != nil {
		return refused, err
	}
	if file.Size == 0 {
		u.emptied = append(u.emptied, file.Key)
		u.skipped = append(u.skipped, skippedFile{RelativePath: path.relative, Reason: skippedEmpty,
			Detail: fmt.Sprintf("%q is empty.", path.name)})
		return nil, nil
	}
	u.staged = append(u.staged, FolderFile{Staged: file, RelativePath: path.relative, Folders: path.folders})
	return nil, nil
}

// complete is the checks that can only be made once the whole form has
// been read.
func (u *folderReceiving) complete() *refusal {
	if !u.admitted {
		// No file part arrived, so nothing was checked and nothing stored.
		return refuse(http.StatusBadRequest, NoFiles)
	}
	if u.received != len(u.form.paths) {
		return refuse(http.StatusBadRequest, "Mismatch: %d files but %d relative_paths", u.received, len(u.form.paths))
	}
	return nil
}
