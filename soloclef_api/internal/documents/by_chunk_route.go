package documents

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CitedChunks reads what the by-chunk route reads.
type CitedChunks interface {
	CitedChunk(ctx context.Context, chunkID int64, userID string) (*Cited, error)
	RecordOf(ctx context.Context, documentID int64, userID string) (*Record, error)
	ChunkCount(ctx context.Context, documentID int64, userID string) (int64, error)
	ChunksBefore(ctx context.Context, cited Cited, userID string) (int64, error)
	ChunkPage(ctx context.Context, documentID int64, userID string, limit, offset int64) ([]Passage, error)
}

// ByChunkRoute serves GET /chunks/{chunk_id}/document: the document a
// citation points into, with the cited chunk and a window of its neighbours.
//
// The Python tier's path was /documents/by-chunk/{chunk_id}, which Go's
// ServeMux cannot hold beside /documents/{document_id}/chunks: the path
// /documents/by-chunk/chunks matches both and neither is more specific, so
// the process panicked at start-up and citations had no route here. The
// chunk is what the request names, so it is the resource the path starts
// from, and the two clients ask this path.
type ByChunkRoute struct {
	Chunks CitedChunks
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register adds the operation.
//
// The path parameter is declared as a string because the refusal for an id
// that is not a whole number has to be the Python tier's 422 envelope, word
// for word, and a schema violation would be answered by Huma in its own
// words instead. `chunk_window` is not declared at all, for the same reason,
// and is read off the query string by hand.
func (r ByChunkRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-document-by-chunk",
		Method:      http.MethodGet,
		Path:        ByChunkPath,
		Summary:     "The document a chunk belongs to, with the chunks around it",
		Description: "The cited chunk's document, the chunk and up to chunk_window chunks either side of it.",
		Tags:        []string{"documents"},
	}, func(ctx context.Context, _ *struct {
		ChunkID string `path:"chunk_id" doc:"The chunk's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ByChunkRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "the document of a chunk could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read if the wiring is ever wrong.
		failed.uncaught(noCaller, nil)
		return
	}

	// The 422 first, before anything is read: FastAPI validates the whole
	// signature before the route's first line runs, so a bad parameter is
	// refused whoever asked and whatever the id names.
	requested := ctx.URL()
	chunk, window, invalid := byChunkParameters(ctx.Param("chunk_id"), requested.Query())
	if len(invalid) > 0 {
		r.Errors.WriteValidation(response, requestID, invalid)
		return
	}

	// The chunk is the first statement and it carries the id, so an id too
	// wide to send fails here, before any refusal.
	if chunk.tooWide() {
		failed.caught(chunkTooWide, errTooWide)
		return
	}
	cited, err := r.Chunks.CitedChunk(ctx.Context(), chunk.value, caller.UserID)
	if err != nil {
		failed.caught("could not read the cited chunk", err)
		return
	}
	if cited == nil {
		r.Errors.Write(response, http.StatusNotFound, chunkAbsent(chunk.value), requestID)
		return
	}

	record, err := r.Chunks.RecordOf(ctx.Context(), cited.DocumentID, caller.UserID)
	if err != nil {
		failed.caught(documentUnread, err)
		return
	}
	if record == nil {
		r.Errors.Write(response, http.StatusNotFound, documentAbsent, requestID)
		return
	}

	total, err := r.Chunks.ChunkCount(ctx.Context(), record.ID, caller.UserID)
	if err != nil {
		failed.caught("could not count the document's chunks", err)
		return
	}
	before, err := r.Chunks.ChunksBefore(ctx.Context(), *cited, caller.UserID)
	if err != nil {
		failed.caught("could not place the cited chunk in its document", err)
		return
	}
	start, end := windowBounds(total, before, window)
	// `.limit(end - start)`, whatever that comes to. The two counts are two
	// statements, so a document losing chunks between them can make it
	// negative; Postgres refuses a negative LIMIT there and refuses it here.
	passages, err := r.Chunks.ChunkPage(ctx.Context(), record.ID, caller.UserID, end-start, start)
	if err != nil {
		failed.caught("could not read the chunks around the cited one", err)
		return
	}

	// The model is built inside the route's try, so a row it cannot be built
	// from is the route's own 500 -- after every statement above has run.
	document, err := buildDocumentWithChunks(*record, passages, total, start)
	if err != nil {
		failed.caught("the document could not be rendered as the Python route renders it", err)
		return
	}
	body, err := pythonjson.Marshal(document)
	if err != nil {
		// Serialisation is after the route returned, where nothing catches.
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// chunkAbsent is `f"Chunk with id {chunk_id} not found"`, which names the id
// as Python holds it after coercion: `704.0` and ` 704 ` are both 704, and a
// negative id keeps its sign.
func chunkAbsent(chunkID int64) string {
	return chunkAbsentPrefix + strconv.FormatInt(chunkID, 10) + chunkAbsentSuffix
}

// byChunkParameters reads get_document_by_chunk_id's two parameters in its
// signature's order -- the path's `chunk_id`, then the query's
// `chunk_window: int = Query(5, ge=0)` -- and reports every failure at once,
// as FastAPI does.
func byChunkParameters(chunkText string, query url.Values) (chunk, window number, failures []errorenvelope.Failure) {
	failures = make([]errorenvelope.Failure, 0, 2)
	chunk, _ = readNumber(chunkText, &failures, "path", "chunk_id")
	window = number{value: defaultChunkWindow}
	if read, named := readBounded(query, "chunk_window", 0, nil, &failures); named {
		window = read
	}
	return chunk, window, failures
}
