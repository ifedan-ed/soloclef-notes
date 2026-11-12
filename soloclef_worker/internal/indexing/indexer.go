// Package indexing is the job that makes a stored document searchable: its
// file is fetched, parsed to Markdown, cut into chunks, embedded, and written
// to Postgres and Qdrant.
//
// # The order, and why it is this order
//
//  1. Load the document row and its file's storage key.
//  2. Fetch the file from the store it was written to, as a stream.
//  3. Parse it with the parser the document names.
//  4. Chunk the Markdown; each chunk knows the span it was cut from.
//  5. Reconcile with the chunks the document already has: an unchanged chunk
//     keeps its row and its vector, and only new text is embedded.
//  6. Embed the new chunks, in batches, through the model proxy.
//  7. Write the new vectors to Qdrant, under ids no row has yet.
//  8. In one transaction: delete the dropped rows, insert the new ones, move
//     the kept ones, store the Markdown, mark the document ready.
//  9. Remove the dropped rows' vectors.
//
// Everything that can fail for a reason outside the document (2, 3, 6, 7)
// happens before anything a reader can see is changed (8). So a document
// being re-indexed stays searchable as it was until the instant it is
// searchable as it will be, and a failed re-index leaves it as it was.
//
// # Run twice, or run again after a crash
//
// Every step is safe to repeat. A crash before step 8 leaves the document
// untouched (and at most some vectors no row points at, which no search can
// return, because every search re-reads its hits from Postgres). A crash
// after step 8 leaves the document ready; the job run again finds every
// chunk unchanged, embeds nothing, and commits the same rows. Step 9 run
// twice deletes nothing the second time.
//
// # Failures
//
// A failure that may pass (the parser service, the proxy, Qdrant or the
// database did not answer; a rate limit) hands the job back to the queue,
// up to Config.MaxAttempts times, and then fails the document. Any other
// failure (a file with no text, a format the parser does not read, a key
// that is refused) fails the document at once. A failed document carries a reason
// written for the person looking at it, and the technical error goes to the
// log. Nothing is ever indexed by a different parser or a different model
// because the chosen one failed.
//
// # A document with no text
//
// An uploaded file that yields no text is a failure: the person is told the
// file has none, and a re-index that finds none keeps what was searchable.
// A document with no file is its own text (a note), and its owner deleting
// all of that text is an edit like any other: the document becomes ready
// with no chunks, and the passages it had leave search with their rows and
// their vectors. Failing it instead would leave the old passages found by
// every search, under a document that no longer says them.
package indexing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/pictures"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/locate"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/vectorindex"
)

// ErrRetry means the job did not finish and should be delivered again. The
// document has been left as it was.
var ErrRetry = errors.New("indexing: the job should be delivered again")

// Documents is the Postgres side of the job (indexstore.Store).
type Documents interface {
	Load(ctx context.Context, documentID int64) (indexstore.Document, error)
	Begin(ctx context.Context, documentID int64, stage string, attempt int) error
	Stage(ctx context.Context, documentID int64, stage string) error
	Fail(ctx context.Context, documentID int64, reason string) error
	Chunks(ctx context.Context, documentID int64) ([]indexstore.ExistingChunk, error)
	AllocateChunkIDs(ctx context.Context, count int) ([]int64, error)
	Replace(ctx context.Context, replacement indexstore.Replacement) ([]int64, error)
}

// Files finds the store a file's row says it was written to
// (*filestore.Stores). A file is only ever looked for in that store.
type Files interface {
	Named(backend string) (filestore.Store, error)
}

// Parsers routes a file to the chosen parser (*selection.Registry).
type Parsers interface {
	Default() selection.Selection
	Parse(ctx context.Context, chosen selection.Selection, source parse.Source) (parse.Document, error)
}

// Piece is one chunk and the span of the Markdown its content was cut from.
type Piece struct {
	Text       string
	Start, End int
}

// Chunker cuts Markdown into chunks (chunking.Chunker, over soloclef_chunker).
type Chunker interface {
	Pieces(markdown string) ([]Piece, error)
}

// Embedder returns one vector per text, in order (embedding.Client).
type Embedder interface {
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
}

// Vectors is the Qdrant side of the job (vectorindex.Client).
type Vectors interface {
	Upsert(ctx context.Context, points []vectorindex.Point) error
	Delete(ctx context.Context, chunkIDs []int64) error
	Present(ctx context.Context, chunkIDs []int64) (map[int64]bool, error)
}

// Config is what this job is given by the deployment. Only VisionModel has
// no value to require: a deployment may have no vision model of its own.
type Config struct {
	// MaxAttempts is how many times a worker may start on one document
	// before a failure that might pass is treated as one that will not.
	MaxAttempts int
	// RetryDelay is how long a job waits before it is handed back to the
	// queue, so a service that is down is not asked again in a tight loop.
	RetryDelay time.Duration
	// Vision opens the vision model that describes a document's pictures,
	// for a document whose upload asked for it: a proxy name, or a model
	// of the owner's own connection (ResolvedVision).
	Vision VisionModels
	// VisionModel is the deployment's vision model, for an owner who chose
	// none. "" is none, and such a document fails with a reason saying so.
	VisionModel string
}

// VisionModels opens an owner's vision model by its stored name.
type VisionModels interface {
	Open(ctx context.Context, userID, name string) (pictures.Describer, error)
}

// ResolvedVision is VisionModels over the one rule that reads a stored
// choice (ownmodels.VisionResolver): the proxy's model by that name, or a
// model of the owner's own connection, called at its own address with its
// key, its headers and the egress guard.
type ResolvedVision struct {
	Resolver ownmodels.VisionResolver
}

func (r ResolvedVision) Open(ctx context.Context, userID, name string) (pictures.Describer, error) {
	return r.Resolver.Open(ctx, userID, name)
}

// Indexer runs the job.
type Indexer struct {
	documents Documents
	files     Files
	parsers   Parsers
	chunker   Chunker
	embedder  Embedder
	vectors   Vectors
	config    Config
	log       *slog.Logger
}

// New refuses a job with a part missing.
func New(documents Documents, files Files, parsers Parsers, chunker Chunker, embedder Embedder, vectors Vectors, config Config, log *slog.Logger) (*Indexer, error) {
	switch {
	case documents == nil || files == nil || parsers == nil || chunker == nil || embedder == nil || vectors == nil || log == nil || config.Vision == nil:
		return nil, errors.New("indexing: a dependency is missing")
	case config.MaxAttempts < 1:
		return nil, fmt.Errorf("indexing: the attempt limit must be at least 1, got %d", config.MaxAttempts)
	case config.RetryDelay < 0:
		return nil, fmt.Errorf("indexing: the retry delay cannot be negative, got %v", config.RetryDelay)
	}
	return &Indexer{documents, files, parsers, chunker, embedder, vectors, config, log}, nil
}

// Outcome is how a job ended.
type Outcome string

const (
	// Indexed: the document is ready and searchable.
	Indexed Outcome = "indexed"
	// Skipped: the document is gone or being deleted; nothing was done.
	Skipped Outcome = "skipped"
	// Failed: the document was marked failed with a reason.
	Failed Outcome = "failed"
	// Retry: the job was handed back; Index returned ErrRetry.
	Retry Outcome = "retry"
)

// Result is what a job did, for the log, the metrics and the benchmarks.
type Result struct {
	Outcome Outcome
	// Reason is what a failed document was told.
	Reason string
	// Bytes is the size of the file fetched; Chunks how many chunks the
	// document has now; Embedded how many of them were sent to the model
	// (the rest kept their vectors).
	Bytes    int64
	Chunks   int
	Embedded int
	Timings  Timings
}

// Index runs the job for one document. It returns ErrRetry (wrapped) when
// the job should be delivered again, and nil otherwise: a document that was
// marked failed is a finished job.
func (i *Indexer) Index(ctx context.Context, documentID int64) (result Result, err error) {
	started := time.Now()
	// result is the named return, so this runs after every return below has
	// set it.
	defer func() { result.Timings.Total = time.Since(started) }()
	log := i.log.With("document", documentID)

	document, err := timed(&result.Timings.Load, func() (indexstore.Document, error) {
		return i.documents.Load(ctx, documentID)
	})
	if errors.Is(err, indexstore.ErrGone) {
		log.Info("nothing to index: the document is gone or being deleted")
		result.Outcome = Skipped
		return result, nil
	}
	if err != nil {
		// The row could not be read, so there is no row to mark either.
		log.Warn("could not read the document; handing the job back", "error", err)
		return i.handBack(ctx, &result, err)
	}

	// A document whose earlier attempts all ended without a verdict -- the
	// worker was killed each time -- is not started on for ever.
	if document.State == indexstore.StateProcessing && document.Attempt >= i.config.MaxAttempts {
		return i.fail(ctx, log, &result, documentID, problem{
			reason: fmt.Sprintf("Indexing was started %d times and never finished. Try again; if it keeps happening, the file may be one the indexer cannot handle.", document.Attempt),
			err:    errors.New("the attempt limit was reached before this attempt began"),
		})
	}
	attempt := 1
	if document.State == indexstore.StateProcessing {
		attempt = document.Attempt + 1
	}
	if err = i.documents.Begin(ctx, documentID, indexstore.StageParsing, attempt); err != nil {
		if errors.Is(err, indexstore.ErrGone) {
			result.Outcome = Skipped
			return result, nil
		}
		log.Warn("could not mark the document processing; handing the job back", "error", err)
		return i.handBack(ctx, &result, err)
	}

	if trouble := i.run(ctx, log, document, &result); trouble != nil {
		switch {
		case errors.Is(trouble.err, indexstore.ErrGone):
			// Deleted while it was being indexed. Whatever was written is
			// the delete's to remove, and there is no row to mark.
			log.Info("the document was deleted while it was being indexed")
			result.Outcome = Skipped
			return result, nil
		case ctx.Err() != nil:
			// The worker is stopping. The document is left processing and
			// the job goes back, so another worker picks it up.
			log.Warn("stopped mid-document; handing the job back", "error", trouble.err)
			result.Outcome = Retry
			return result, fmt.Errorf("%w: %w", ErrRetry, ctx.Err())
		case trouble.retryable && attempt < i.config.MaxAttempts:
			log.Warn("indexing failed for a reason that may pass; handing the job back",
				"attempt", attempt, "of", i.config.MaxAttempts, "error", trouble.err)
			if err := i.documents.Stage(ctx, documentID, indexstore.StageRetrying); err != nil {
				log.Warn("could not record that the document is waiting to be retried", "error", err)
			}
			return i.handBack(ctx, &result, trouble.err)
		case trouble.retryable:
			trouble.reason += fmt.Sprintf(" It was tried %d times.", attempt)
		}
		return i.fail(ctx, log, &result, documentID, *trouble)
	}

	result.Outcome = Indexed
	log.Info("indexed",
		"bytes", result.Bytes, "chunks", result.Chunks, "embedded", result.Embedded,
		"total", time.Since(started).Round(time.Millisecond).String(),
		"fetch", result.Timings.Fetch.Round(time.Millisecond).String(),
		"parse", result.Timings.Parse.Round(time.Millisecond).String(),
		"vision", result.Timings.Vision.Round(time.Millisecond).String(),
		"chunk", result.Timings.Chunk.Round(time.Millisecond).String(),
		"embed", result.Timings.Embed.Round(time.Millisecond).String(),
		"vectors", result.Timings.Vectors.Round(time.Millisecond).String(),
		"write", result.Timings.Write.Round(time.Millisecond).String())
	return result, nil
}

// handBack waits the retry delay and returns ErrRetry. The wait is here, in
// the job, because the queue puts a returned message straight back at its
// tail: with an empty queue that is an immediate redelivery.
func (i *Indexer) handBack(ctx context.Context, result *Result, cause error) (Result, error) {
	result.Outcome = Retry
	timer := time.NewTimer(i.config.RetryDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	return *result, fmt.Errorf("%w: %w", ErrRetry, cause)
}

// fail marks the document failed. If even that cannot be written the job is
// handed back, because a document left "processing" with no job behind it
// would spin in the list for ever.
func (i *Indexer) fail(ctx context.Context, log *slog.Logger, result *Result, documentID int64, trouble problem) (Result, error) {
	log.Error("indexing failed", "reason", trouble.reason, "error", trouble.err)
	// Not the job's context: a document must be told it failed even when the
	// failure is the job's deadline.
	markCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer done()
	if err := i.documents.Fail(markCtx, documentID, trouble.reason); err != nil {
		log.Error("could not mark the document failed; handing the job back", "error", err)
		return i.handBack(ctx, result, err)
	}
	result.Outcome = Failed
	result.Reason = trouble.reason
	return *result, nil
}

// run is steps 2 to 9. It returns nil when the document is ready.
func (i *Indexer) run(ctx context.Context, log *slog.Logger, document indexstore.Document, result *Result) *problem {
	markdown, pages, trouble := i.markdown(ctx, document, result)
	if trouble != nil {
		return trouble
	}

	i.stage(ctx, log, document.ID, indexstore.StageChunking)
	pieces, err := timed(&result.Timings.Chunk, func() ([]Piece, error) { return i.chunker.Pieces(markdown) })
	if err != nil {
		return &problem{reason: "The document could not be split into passages.", err: err}
	}
	if len(pieces) == 0 {
		if document.File != nil {
			return &problem{reason: "The document has no text to index.", err: errors.New("the chunker returned no chunks")}
		}
		return i.emptied(ctx, log, document, markdown, result)
	}
	places := place(markdown, pages, pieces)

	// Which chunks the document already has, and which of those still have
	// their vector. Only a chunk with both is kept.
	plan, err := timed(&result.Timings.Reconcile, func() (plan, error) {
		existing, err := i.documents.Chunks(ctx, document.ID)
		if err != nil {
			return plan{}, err
		}
		ids := make([]int64, len(existing))
		for index, chunk := range existing {
			ids[index] = chunk.ID
		}
		present, err := i.vectors.Present(ctx, ids)
		if err != nil {
			return plan{}, err
		}
		return reconcile(existing, present, pieces), nil
	})
	if err != nil {
		return describe(err)
	}

	i.stage(ctx, log, document.ID, indexstore.StageEmbedding)
	texts := make([]string, len(plan.fresh))
	for index, piece := range plan.fresh {
		texts[index] = pieces[piece].Text
	}
	vectors, err := timed(&result.Timings.Embed, func() ([][]float32, error) {
		return i.embedder.EmbedDocuments(ctx, texts)
	})
	if err != nil {
		return describe(err)
	}
	if len(vectors) != len(texts) {
		return &problem{reason: "The embedding service gave an answer that could not be used.",
			err: fmt.Errorf("%d vectors for %d chunks", len(vectors), len(texts))}
	}

	i.stage(ctx, log, document.ID, indexstore.StageIndexing)
	ids, err := i.documents.AllocateChunkIDs(ctx, len(texts))
	if err != nil {
		return describe(err)
	}
	replacement := indexstore.Replacement{
		DocumentID: document.ID, UserID: document.UserID, Markdown: markdown,
		// The text was the row's own (a note, or a restored version), not
		// a parse of the file: the row keeps it as it now is (indexstore).
		FromSourceMarkdown: document.File == nil || (document.TextIsNewer && document.SourceMarkdown != ""),
		New:                make([]indexstore.NewChunk, len(texts)),
	}
	points := make([]vectorindex.Point, len(texts))
	for index, piece := range plan.fresh {
		replacement.New[index] = indexstore.NewChunk{ID: ids[index], Content: pieces[piece].Text, Place: places[piece]}
		points[index] = vectorindex.Point{
			ChunkID: ids[index], Vector: vectors[index],
			UserID: document.UserID, DocumentType: document.Type, DocumentID: document.ID,
		}
	}
	for piece, id := range plan.kept {
		replacement.Kept = append(replacement.Kept, indexstore.KeptChunk{ID: id, Place: places[piece]})
	}

	// Vectors first, rows second: see indexstore.Store.Replace for why that
	// order is what makes the swap atomic to a reader.
	if _, err := timed(&result.Timings.Vectors, func() (struct{}, error) {
		return struct{}{}, i.vectors.Upsert(ctx, points)
	}); err != nil {
		i.discard(ctx, log, ids)
		return describe(err)
	}
	removed, err := timed(&result.Timings.Write, func() ([]int64, error) {
		return i.documents.Replace(ctx, replacement)
	})
	if err != nil {
		// The rows were not written, so the vectors just written point at
		// nothing and never will: their ids are not handed out again.
		i.discard(ctx, log, ids)
		return describe(err)
	}
	result.Chunks = len(pieces)
	result.Embedded = len(texts)
	i.removeVectors(ctx, log, removed, result)
	return nil
}

// emptied finishes a document that is its own text and now has none: every
// chunk row goes in the one transaction that marks it ready, and then their
// vectors. Nothing is parsed, embedded or written to Qdrant, so none of the
// steps that may fail for a reason outside the document is taken.
func (i *Indexer) emptied(ctx context.Context, log *slog.Logger, document indexstore.Document, markdown string, result *Result) *problem {
	i.stage(ctx, log, document.ID, indexstore.StageIndexing)
	removed, err := timed(&result.Timings.Write, func() ([]int64, error) {
		return i.documents.Replace(ctx, indexstore.Replacement{
			DocumentID: document.ID, UserID: document.UserID, Markdown: markdown,
			// The row's own text, kept as it is (a save made since stays
			// pending for the job it queued).
			FromSourceMarkdown: true,
		})
	})
	if err != nil {
		return describe(err)
	}
	result.Chunks, result.Embedded = 0, 0
	i.removeVectors(ctx, log, removed, result)
	return nil
}

// removeVectors deletes the vectors of chunk rows a replacement removed. The
// document is ready by now and nothing here can fail it: a vector whose row
// is gone cannot be returned by a search, so a delete that fails costs disk
// in Qdrant and nothing else. It is logged, and the scheduled count of rows
// against points is what notices it.
func (i *Indexer) removeVectors(ctx context.Context, log *slog.Logger, removed []int64, result *Result) {
	if _, err := timed(&result.Timings.Cleanup, func() (struct{}, error) {
		return struct{}{}, i.vectors.Delete(ctx, removed)
	}); err != nil {
		log.Warn("the old chunks' vectors could not be removed; they are unreachable but still stored",
			"vectors", len(removed), "error", err)
	}
}

// markdown is steps 2 and 3: the document's Markdown and its page table.
//
// A document with no uploaded file (a note, a crawled page) is indexed from
// the Markdown already on its row, and has no pages.
func (i *Indexer) markdown(ctx context.Context, document indexstore.Document, result *Result) (string, []parse.Page, *problem) {
	// A version restored onto a file's document is the text the person
	// chose; the file would give back the text they replaced. The Python
	// reindex indexed the restored text, and so does this.
	if document.File != nil && document.TextIsNewer && document.SourceMarkdown != "" {
		return document.SourceMarkdown, nil, nil
	}
	if document.File == nil {
		// Its own text, empty included: an emptied note is ready with no
		// chunks (run, and the package comment).
		return document.SourceMarkdown, nil, nil
	}

	chosen := i.parsers.Default()
	if document.Parser != "" {
		var err error
		if chosen, err = selection.Of(document.Parser); err != nil {
			// The column's CHECK refuses any other name, so this is a
			// database the migration has not reached.
			return "", nil, &problem{reason: "The document names a parser this server does not know.", err: err}
		}
	}

	// The file is opened by the parser, when and as often as the parser
	// wants. The source below remembers why an open failed, because a parser
	// reports every failed open the same way ("unavailable", try again) and
	// a file that is not in storage will not be there next time either.
	var fetch fetching
	source := parse.Source{
		Name: document.File.Name,
		Size: document.File.Size,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			store, err := i.files.Named(document.File.Backend)
			if err != nil {
				fetch.fail(err)
				return nil, err
			}
			body, err := store.Open(ctx, document.File.Key)
			if err != nil {
				fetch.fail(err)
				return nil, err
			}
			return &timedBody{body: body, fetch: &fetch}, nil
		},
	}

	// With vision on (the upload asked, and the row keeps it), a picture is
	// its description and a PDF's pictures are described into its text.
	// The model is settled before a byte is fetched: a document that cannot
	// have its pictures described fails at once, and is never indexed
	// without them as if nobody had asked.
	name := document.File.Name
	describePDF := document.Vision && pictures.IsPDF(name)
	var seer pictures.Describer
	if document.Vision && (describePDF || pictures.IsPicture(name)) {
		var trouble *problem
		if seer, trouble = i.visionModel(ctx, document); trouble != nil {
			return "", nil, trouble
		}
	}
	if document.Vision && pictures.IsPicture(name) {
		return i.picture(ctx, document, seer, source, &fetch, result)
	}
	// The PDF is held in memory once: the parser reads it from there and
	// pdfcpu takes its pictures out of the same bytes, so storage is read
	// once and not twice. One too large to hold is parsed as a stream, as
	// any document, and says its pictures were not described.
	var pdf []byte
	if describePDF && document.File.Size <= pictures.MaxPDFBytes {
		held, trouble := hold(ctx, source, &fetch, pictures.MaxPDFBytes)
		if trouble != nil {
			return "", nil, trouble
		}
		pdf, source = held, inMemory(name, held)
	}

	prefetched, _, _ := fetch.totals()
	parsed, err := timed(&result.Timings.Parse, func() (parse.Document, error) {
		return i.parsers.Parse(ctx, chosen, source)
	})
	// Time spent reading the file happened inside the parse; take it out so
	// the two phases are reported apart.
	fetched, size, openFailure := fetch.totals()
	result.Timings.Fetch = fetched
	result.Timings.Parse -= fetched - prefetched
	result.Bytes = size
	if openFailure != nil {
		return "", nil, describeFetch(openFailure)
	}
	if err != nil {
		return "", nil, describe(err)
	}
	switch {
	case describePDF && pdf == nil:
		parsed = pictures.TooLarge(parsed)
	case describePDF:
		var outcome pictures.Outcome
		parsed, err = timed(&result.Timings.Vision, func() (parse.Document, error) {
			merged, done, err := pictures.Describe(ctx, seer, pdf, parsed)
			outcome = done
			return merged, err
		})
		if err != nil {
			return "", nil, describe(err)
		}
		i.log.Info("described the PDF's pictures", "document", document.ID, "described", outcome.Described,
			"failed", outcome.Failed, "too_small", outcome.TooSmall, "duplicate", outcome.Duplicate, "unsendable", outcome.Unsendable)
	}
	return parsed.Markdown, parsed.Pages, nil
}

// visionModel is the model that describes the document's pictures, opened:
// the owner's own choice, a proxy name or a model of their own connection,
// else the deployment's (accountvision.Name, the rule every picture
// follows).
//
// A chosen model of the owner's own connection that cannot be called is
// said so, and the deployment's model is not used in its place. The owner
// chose, and a description by a model nobody chose is a wrong answer that
// looks right.
func (i *Indexer) visionModel(ctx context.Context, document indexstore.Document) (pictures.Describer, *problem) {
	name := accountvision.Name(document.VisionChoice, i.config.VisionModel)
	if name == "" {
		return nil, &problem{
			reason: "The upload asked for its pictures to be described, and no vision model is set for this account or on this server. Choose a vision model in Settings, Models, and index the document again.",
			err:    errors.New("vision was asked for and no vision model is set"),
		}
	}
	opened, err := i.config.Vision.Open(ctx, document.UserID, name)
	if sentence, said := accountvision.Sentence(err); said {
		// Removed, switched off, a key that no longer opens: the person
		// mends it, then asks again. Trying again unchanged would not.
		return nil, &problem{reason: sentence + " Then index the document again.", err: err}
	}
	if err != nil {
		// The connection's row or the operator's allowed hosts could not
		// be read: a fault of the moment.
		return nil, &problem{reason: "The vision model could not be opened just now, so the document's pictures could not be described.", retryable: true, err: err}
	}
	return opened, nil
}

// picture indexes an uploaded picture as its description.
func (i *Indexer) picture(ctx context.Context, document indexstore.Document, seer pictures.Describer, source parse.Source, fetch *fetching, result *Result) (string, []parse.Page, *problem) {
	if document.File.Size > vision.MaxConvertibleBytes {
		return "", nil, &problem{
			reason: fmt.Sprintf("The picture is %.1f MB, over the %d MB a picture may be to be described.", float64(document.File.Size)/(1<<20), vision.MaxConvertibleBytes>>20),
			err:    errors.New("the picture is over the vision limit"),
		}
	}
	data, trouble := hold(ctx, source, fetch, vision.MaxConvertibleBytes)
	result.Timings.Fetch, result.Bytes, _ = fetch.totals()
	if trouble != nil {
		return "", nil, trouble
	}
	described, err := timed(&result.Timings.Vision, func() (parse.Document, error) {
		return pictures.Picture(ctx, seer, document.File.Name, data)
	})
	if err != nil {
		return "", nil, describe(err)
	}
	return described.Markdown, nil, nil
}

// hold reads the whole file into memory, at most most bytes.
func hold(ctx context.Context, source parse.Source, fetch *fetching, most int64) ([]byte, *problem) {
	body, err := source.Open(ctx)
	if err != nil {
		return nil, describeFetch(err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, most+1))
	if err != nil {
		return nil, describeFetch(err)
	}
	if int64(len(data)) > most {
		// The row said it was smaller: the stored file is not the one the
		// row describes.
		return nil, &problem{reason: "The stored file is larger than its record says. Upload it again.", err: fmt.Errorf("more than %d bytes", most)}
	}
	return data, nil
}

// inMemory is a source over bytes already read.
func inMemory(name string, data []byte) parse.Source {
	return parse.Source{Name: name, Size: int64(len(data)), Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}}
}

// stage records progress for whoever is watching the upload. It is a
// courtesy: a failure to record it must not fail an indexing that is working.
func (i *Indexer) stage(ctx context.Context, log *slog.Logger, documentID int64, stage string) {
	if err := i.documents.Stage(ctx, documentID, stage); err != nil {
		log.Warn("could not record the stage", "stage", stage, "error", err)
	}
}

// discard removes vectors this job wrote for rows it then did not write.
func (i *Indexer) discard(ctx context.Context, log *slog.Logger, ids []int64) {
	if len(ids) == 0 {
		return
	}
	cleanCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer done()
	if err := i.vectors.Delete(cleanCtx, ids); err != nil {
		log.Warn("vectors written for rows that were not committed could not be removed; they are unreachable but still stored",
			"vectors", len(ids), "error", err)
	}
}

// place computes every chunk's position, line range and page range.
func place(markdown string, pages []parse.Page, pieces []Piece) []indexstore.Place {
	lines := locate.NewLines(markdown)
	places := make([]indexstore.Place, len(pieces))
	for index, piece := range pieces {
		first, last := lines.Range(piece.Start, piece.End)
		places[index] = indexstore.Place{Position: index, StartLine: first, EndLine: last}
		// The page is where the chunk's content is, not the white space
		// around it: a chunk that ends with the blank line before a page
		// break is not on the next page.
		start, end := lines.Content(piece.Start, piece.End)
		if from, to, found := locate.Pages(pages, start, end); found {
			places[index].PageStart, places[index].PageEnd = &from, &to
		}
	}
	return places
}

// timed runs a step and adds how long it took to *into.
func timed[T any](into *time.Duration, step func() (T, error)) (T, error) {
	started := time.Now()
	value, err := step()
	*into += time.Since(started)
	return value, err
}
