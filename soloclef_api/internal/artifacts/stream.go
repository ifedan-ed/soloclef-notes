package artifacts

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/rollingdeadline"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_filestore/objecthttp"
)

// This file is the three routes that send an artifact's bytes: the download
// button, a file's content (what the PDF viewer, the video player and the
// deck and quiz viewers fetch), and one video slide's narration.
//
// They differ only in *which* object and *under which headers*. How the
// bytes travel -- length, ranges, validators, never in memory -- is
// objecthttp's, and is the same for all three.
//
// How *long* they may take is this file's, and is the one thing a JSON route
// never has to think about: a video on a slow link outlasts any timeout that
// suits the rest of the server. Each route is therefore given a StallTimeout
// and sends its body through internal/rollingdeadline, which holds a client
// to "keep taking bytes" rather than to "be finished within a minute".

// requireStallTimeout stops the server from starting with a byte route that
// was not told how long a stalled client may be kept. There is no default to
// fall back on: zero would mean "every write is already late", and a guessed
// number here would silently disagree with the server's own write timeout,
// which is what cmd/api passes.
func requireStallTimeout(route string, stall time.Duration) {
	if stall <= 0 {
		panic("artifacts: " + route + " needs a StallTimeout greater than zero")
	}
}

// What a caller is told when the bytes cannot be sent.
const (
	fileAbsent       = "Artifact file not found"
	slideAudioAbsent = "Slide audio file not found"
)

// The cache rules, one per kind of URL.
const (
	// A file's content URL names one `artifact_files` row, and a row's bytes
	// never change: a revised artifact gets new rows and new ids. So a
	// browser may keep it for as long as it likes and need never ask again.
	// `private` because the answer depended on who asked.
	immutableForever = "private, max-age=31536000, immutable"
	// A slide's audio URL names a *slide number*, and a regenerated video
	// puts different narration behind the same number. So the browser may
	// keep it but must check it is still current -- a cheap 304 when it is.
	revalidate = "private, no-cache"
)

// Downloads reads what the download sends.
type Downloads interface {
	DownloadOf(ctx context.Context, userID string, artifactID int64) (*Download, error)
}

// DownloadRoute serves the download button: the artifact's primary file as
// an attachment named after its title, or -- for an artifact that is only a
// document -- that document as Markdown.
type DownloadRoute struct {
	Gate
	Downloads Downloads
	// Files is every store this process reads; the row says which one.
	Files *filestore.Stores
	// StallTimeout is how long the client may accept nothing before the
	// download is dropped. However long the download takes otherwise is the
	// client's business.
	StallTimeout time.Duration
}

// Register adds the operation. The path parameters are declared as strings,
// here and on every route below, so that an id that is not a whole number is
// refused with this tier's own 422 envelope rather than with Huma's.
func (r DownloadRoute) Register(api huma.API) {
	requireStallTimeout("DownloadRoute", r.StallTimeout)
	huma.Register(api, huma.Operation{
		OperationID: "download-artifact",
		Method:      http.MethodGet,
		Path:        DownloadPath,
		Summary:     "Download an artifact",
		Tags:        []string{"artifacts"},
	}, func(ctx context.Context, _ *struct {
		ArtifactID string `path:"artifact_id" doc:"The artifact's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DownloadRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	request, ok := r.admit(ctx, response, nil)
	if !ok {
		return
	}
	found, err := r.Downloads.DownloadOf(ctx.Context(), request.caller.UserID, request.artifactID)
	if err != nil {
		r.fail(ctx.Context(), response, request.requestID, "could not read the artifact", err)
		return
	}
	if found == nil {
		r.Errors.Write(response, http.StatusNotFound, artifactAbsent, request.requestID)
		return
	}

	if found.File == nil {
		// No file: the artifact *is* its document. Sent with a length like
		// everything else; it came out of one row and is already in memory.
		//
		// It is paced like a file all the same. A long document is megabytes,
		// and on a slow link megabytes outlast the server's write timeout
		// just as a video does.
		body := []byte(found.Markdown)
		steady, ok := r.paced(ctx.Context(), response, request.requestID, r.StallTimeout)
		if !ok {
			return
		}
		steady.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		steady.Header().Set("Content-Length", strconv.Itoa(len(body)))
		steady.Header().Set("Content-Disposition", contentDisposition(markdownName(found.Title), false))
		steady.Header().Set("Cache-Control", privateNoStore)
		steady.Header().Set("X-Content-Type-Options", "nosniff")
		steady.WriteHeader(http.StatusOK)
		sent, err := steady.Write(body)
		r.streamEnded(ctx.Context(), request.requestID, "", steady, ended{
			sent: int64(sent), promised: int64(len(body)), err: err,
		})
		return
	}

	r.stream(ctx, response, request.requestID, r.Files, r.StallTimeout, storedObject{
		backend: found.File.Backend,
		key:     found.File.Key,
		absent:  fileAbsent,
		headers: objecthttp.Headers{
			ContentType: typeOf(found.File.MimeType),
			ETag:        tagOf(found.File.Checksum),
			// A download is always the current version, fetched because
			// somebody pressed a button; nothing should answer it from a
			// cache.
			CacheControl: privateNoStore,
			// Always an attachment, whatever the type: this route's whole
			// purpose is "save this".
			Disposition: contentDisposition(downloadName(found.Title, found.File.Filename), false),
		},
	})
}

// FileRows reads one file's row.
type FileRows interface {
	FileIn(ctx context.Context, userID string, artifactID, fileID int64) (*StoredFile, error)
}

// FileContentRoute serves one stored file by its id: the URL the manifest
// hands out as `content_url`. This is the route players seek through, so it
// is the one where Range and the 206 matter most.
type FileContentRoute struct {
	Gate
	Rows  FileRows
	Files *filestore.Stores
	// StallTimeout is how long the client may accept nothing before the
	// stream is dropped.
	StallTimeout time.Duration
}

func (r FileContentRoute) Register(api huma.API) {
	requireStallTimeout("FileContentRoute", r.StallTimeout)
	huma.Register(api, huma.Operation{
		OperationID: "read-artifact-file-content",
		Method:      http.MethodGet,
		Path:        FileContentPath,
		Summary:     "The bytes of one of an artifact's files",
		Tags:        []string{"artifacts"},
	}, func(ctx context.Context, _ *struct {
		ArtifactID string `path:"artifact_id" doc:"The artifact's id"`
		FileID     string `path:"file_id" doc:"The file's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r FileContentRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	var fileID int64
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			fileID = readID(ctx.Param("file_id"), failures, "path", "file_id")
		})
	if !ok {
		return
	}
	file, err := r.Rows.FileIn(ctx.Context(), request.caller.UserID, request.artifactID, fileID)
	if err != nil {
		r.fail(ctx.Context(), response, request.requestID, "could not read the artifact's file", err)
		return
	}
	if file == nil {
		r.Errors.Write(response, http.StatusNotFound, fileAbsent, request.requestID)
		return
	}

	mediaType := typeOf(file.MimeType)
	r.stream(ctx, response, request.requestID, r.Files, r.StallTimeout, storedObject{
		backend: file.Backend,
		key:     file.Key,
		absent:  fileAbsent,
		headers: objecthttp.Headers{
			ContentType:  mediaType,
			ETag:         tagOf(file.Checksum),
			CacheControl: immutableForever,
			Disposition:  contentDisposition(file.Filename, inlineTypes[mediaType]),
		},
	})
}

// SlideRows reads a video's slides.
type SlideRows interface {
	SlidesOf(ctx context.Context, userID string, artifactID int64) (*SlideSet, error)
}

// SlideAudioRoute serves the narration of one slide of a video artifact: the
// URL the video payload hands the player as each slide's `audio_url`.
type SlideAudioRoute struct {
	Gate
	Slides SlideRows
	Files  *filestore.Stores
	// StallTimeout is how long the client may accept nothing before the
	// stream is dropped.
	StallTimeout time.Duration
}

func (r SlideAudioRoute) Register(api huma.API) {
	requireStallTimeout("SlideAudioRoute", r.StallTimeout)
	huma.Register(api, huma.Operation{
		OperationID: "read-artifact-slide-audio",
		Method:      http.MethodGet,
		Path:        SlideAudioPath,
		Summary:     "The narration of one slide of a video artifact",
		Tags:        []string{"artifacts"},
	}, func(ctx context.Context, _ *struct {
		ArtifactID  string `path:"artifact_id" doc:"The artifact's id"`
		SlideNumber string `path:"slide_number" doc:"The slide's number"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r SlideAudioRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	var slideNumber int64
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			slideNumber = readID(ctx.Param("slide_number"), failures, "path", "slide_number")
		})
	if !ok {
		return
	}
	found, err := r.Slides.SlidesOf(ctx.Context(), request.caller.UserID, request.artifactID)
	if err != nil {
		r.fail(ctx.Context(), response, request.requestID, "could not read the artifact", err)
		return
	}
	if found == nil {
		r.Errors.Write(response, http.StatusNotFound, artifactAbsent, request.requestID)
		return
	}
	if found.Format != videoFormat {
		r.Errors.Write(response, http.StatusNotFound, notAVideo, request.requestID)
		return
	}
	number := strconv.FormatInt(slideNumber, 10)
	slide := slideNumbered(found.Slides, number)
	if slide == nil {
		r.Errors.Write(response, http.StatusNotFound, "Slide "+number+" not found", request.requestID)
		return
	}
	// A slide rendered before narration was moved into the file store names
	// only a path on the machine that rendered it (`audio_file`). That file
	// is not in the store and is not served from wherever it was.
	key, _ := slide.GetOrNil("audio_storage_key").(string)
	if !present(key) {
		r.Errors.Write(response, http.StatusNotFound, slideAudioAbsent, request.requestID)
		return
	}
	// A slide records its backend beside its key, and that record is what is
	// followed. One written before the backend was recorded names none, and
	// then the bytes are looked for in the configured backend -- the one
	// place the writer itself looks when it reads them back
	// (app/artifacts/media/video/storage.py). That is the meaning of an
	// absent record, not a second try: a slide that does name a backend is
	// read from that one and no other.
	backend := r.Files.Configured()
	if recorded, named := slide.GetOrNil("storage_backend").(string); named && recorded != "" {
		backend = recorded
	}

	// The writer stores WAV or MP3 and nothing else, and names the key with
	// the matching extension (app/artifacts/media/video/storage.py).
	extension, mediaType := ".mp3", "audio/mpeg"
	if strings.EqualFold(path.Ext(key), ".wav") {
		extension, mediaType = ".wav", "audio/wav"
	}
	r.stream(ctx, response, request.requestID, r.Files, r.StallTimeout, storedObject{
		backend: backend,
		key:     key,
		absent:  slideAudioAbsent,
		headers: objecthttp.Headers{
			ContentType: mediaType,
			// No checksum is kept for a slide's audio, so the validator is
			// the one objecthttp derives from the stored object.
			CacheControl: revalidate,
			// Named for what it is. The storage key's own filename used to
			// be sent here, which told every viewer how the store is laid
			// out and looked like noise in a download dialog.
			Disposition: contentDisposition("slide-"+number+extension, true),
		},
	})
}

// slideNumbered is the stored slide with this number, or nil. The number is
// compared as digits against a JSON integer: a slide whose number was stored
// as text, a float or a truth has no number.
func slideNumbered(slides []byte, number string) *pythonjson.Object {
	list, _ := decodeColumn(slides).([]any)
	for _, entry := range list {
		slide, isObject := entry.(*pythonjson.Object)
		if !isObject {
			continue
		}
		if stored, isInteger := integer(slide.GetOrNil("slide_number")); isInteger && string(stored) == number {
			return slide
		}
	}
	return nil
}

// typeOf is a row's media type, or plain bytes when the row names none.
func typeOf(mimeType string) string {
	if strings.TrimSpace(mimeType) == "" {
		return fallbackType
	}
	return mimeType
}

// tagOf is a file's HTTP validator: its SHA-256, quoted. A row without one
// (none is written today; the column is NOT NULL) yields no tag here, and
// objecthttp then derives one from the stored object.
func tagOf(checksum string) string {
	if checksum == "" {
		return ""
	}
	return `"` + checksum + `"`
}

// storedObject is one object to send and how.
type storedObject struct {
	// backend is the store the row says holds the bytes.
	backend string
	key     string
	headers objecthttp.Headers
	// absent is what the caller is told if the bytes are not there.
	absent string
}

// stream sends one stored object, or the refusal for one that cannot be.
func (g Gate) stream(ctx huma.Context, response http.ResponseWriter, requestID string,
	stores *filestore.Stores, stall time.Duration, object storedObject) {
	// The row says which backend holds the bytes, and they are read from
	// that one: a file written to local disk before the deployment moved to
	// a bucket is still read off the disk. If this process was given no
	// settings for the backend the row names, the bytes are somewhere it
	// cannot reach, and the honest answer is a logged failure -- not a look
	// in a store it does have, which could find a different object under
	// the same key.
	store, err := stores.Named(object.backend)
	if err != nil {
		// Passed as the reason and not as the cause: a failure with a cause
		// is logged as the client's leaving when the client has left, and
		// this one is true whoever is listening.
		g.fail(ctx.Context(), response, requestID, "a stored file cannot be read: "+err.Error(), nil)
		return
	}

	answer, err := objecthttp.Prepare(ctx.Context(), store, object.key, objecthttp.Request{
		Method:      ctx.Method(),
		Range:       ctx.Header("Range"),
		IfNoneMatch: ctx.Header("If-None-Match"),
		IfRange:     ctx.Header("If-Range"),
	}, object.headers)
	switch {
	case errors.Is(err, filestore.ErrNotFound), errors.Is(err, filestore.ErrInvalidKey):
		// A row whose bytes are gone, or whose key was never a key. To the
		// caller both are "that file is not here"; to whoever runs the
		// server both are data loss, so it is logged as an error with the
		// key that would find it.
		g.Logger.LogAttrs(ctx.Context(), slog.LevelError, "a stored file is missing from the file store",
			slog.String("request_id", requestID),
			slog.String("storage_key", object.key),
			slog.String("error", err.Error()))
		g.Errors.Write(response, http.StatusNotFound, object.absent, requestID)
		return
	case err != nil:
		g.fail(ctx.Context(), response, requestID, "could not open a stored file", err)
		return
	}

	// The last thing that can still be answered with an error, so it is done
	// before the status: the response is given its rolling write deadline.
	steady, ok := g.paced(ctx.Context(), response, requestID, stall)
	if !ok {
		answer.Close()
		return
	}

	// From here the status line is on the wire, so a failure has nobody to be
	// reported to. It can only be logged, and streamEnded says at what level.
	sent, err := answer.Send(steady)
	result := ended{sent: sent, promised: sent, err: err}
	// A GET answered 200 or 206 promised a body of Content-Length bytes; a
	// HEAD, a 304 and a 416 promised none, and sent none.
	if ctx.Method() != http.MethodHead &&
		(answer.Status() == http.StatusOK || answer.Status() == http.StatusPartialContent) {
		result.promised, _ = strconv.ParseInt(steady.Header().Get("Content-Length"), 10, 64)
	}
	g.streamEnded(ctx.Context(), requestID, object.key, steady, result)
}

// paced gives the response a write deadline that moves with the body
// (internal/rollingdeadline, which says why the server's own timeout cannot
// be used for a stream).
//
// If the deadline cannot be moved, the route answers 500 and says why in the
// log. It does not stream under the server's fixed timeout instead: that is
// the cut-at-sixty-seconds defect, and falling back to it quietly is how it
// would return unnoticed the day somebody adds a response wrapper without
// `Unwrap`. A loud failure on the first request is found the same day.
func (g Gate) paced(ctx context.Context, response http.ResponseWriter, requestID string,
	stall time.Duration) (*rollingdeadline.Writer, bool) {
	steady, err := rollingdeadline.Wrap(response, stall)
	if err != nil {
		g.fail(ctx, response, requestID, "could not pace a stream", err)
		return nil, false
	}
	return steady, true
}

// ended is how a stream finished.
type ended struct {
	// sent is the body bytes written, and promised is the Content-Length
	// they were written under.
	sent, promised int64
	err            error
}

// streamEnded logs a stream that did not finish, at the level of whose doing
// it was. The status and the length are already on the wire, so the log is
// the only place any of this can be said.
//
// The levels used to be the wrong way round: everything that went wrong
// mid-stream was INFO, on the reasoning that it is nearly always the viewer
// closing the tab -- which hid the server's own failures among the tabs.
// They are told apart here:
//
//   - the client left (closed the tab, sought elsewhere in the video,
//     dropped off the network): INFO. Nothing is wrong.
//   - the client stopped reading and was dropped after the stall timeout:
//     WARN. The server did this, on purpose; one is a bad connection, a
//     hundred from one address is somebody holding sockets open.
//   - the store failed, or handed over fewer bytes than it said it had:
//     ERROR. A viewer got a truncated file through no fault of theirs.
func (g Gate) streamEnded(ctx context.Context, requestID, key string,
	steady *rollingdeadline.Writer, result ended) {
	if result.err == nil && result.sent == result.promised {
		return
	}
	level, message := slog.LevelError, "a stored file failed mid-stream"
	switch {
	case result.err == nil:
		// No error and too few bytes: the object is shorter than the store
		// said when it was asked for its size.
		message = "a stored file ended before its promised length"
	case steady.Stalled():
		level, message = slog.LevelWarn, "a client that stopped reading a stream was dropped"
	case steady.ClientError() != nil:
		level, message = slog.LevelInfo, "the client left mid-stream"
	case ctx.Err() != nil:
		// The source failed, but only after the request was over: a read
		// from a remote store is abandoned when its client is, and reports
		// that as an error of its own.
		level, message = slog.LevelInfo, "the client left mid-stream"
	}
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.Int64("bytes_sent", result.sent),
		slog.Int64("bytes_promised", result.promised),
	}
	if key != "" {
		attributes = append(attributes, slog.String("storage_key", key))
	}
	if result.err != nil {
		attributes = append(attributes, slog.String("error", result.err.Error()))
	}
	g.Logger.LogAttrs(ctx, level, message, attributes...)
}
