package notebookresearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
)

const (
	// readTimeout bounds one import's read: the page reader's own limits
	// for a page and then a PDF's body (twenty seconds and two minutes,
	// soloclef_assistant/assembly), or PubMed Central's paced answer, with
	// room to spare.
	readTimeout = 3 * time.Minute
	// paperChars is how much of a paper's open full text becomes the
	// source. A long paper is about a hundred thousand characters; one far
	// over that is a book, and its opening is what a source needs.
	paperChars = 200_000
	// recordTimeout bounds the statement that records how a read ended,
	// which runs even when the server is stopping.
	recordTimeout = 10 * time.Second
	// staleAfter is when a document still waiting to be read is taken to
	// have no reader behind it, so that importing it again reads it. It is
	// several times readTimeout: a read still running is never doubled.
	staleAfter = 10 * time.Minute

	// interrupted is the reason on a document whose read the server's stop
	// cut short. It is written for the person looking at the source list.
	interrupted = "The page was not read because the server restarted. Import it again to read it."
)

// Reads fills imported documents with their text, in this process, a few
// at a time.
//
// A read is a goroutine and not a queued job because the reading is this
// tier's own (the page reader the chat uses, with its guard on where it may
// connect), and a queue would only move the same call to another process.
// What a queue would have given, surviving a restart, is covered another
// way: Stop marks a cut read failed with a reason that says to import
// again, and the import requeues a document that was never read
// (RequeueUnread).
type Reads struct {
	pool     *pgxpool.Pool
	pages    PageReader
	papers   Papers
	indexing Indexing
	// originals is where an address that is a PDF is kept (originals.go).
	originals Originals
	logger    *slog.Logger
	// lifetime ends when the server stops: every read under way stops.
	lifetime context.Context
	// slots bounds how many reads run at once, so one import of fifty
	// pages opens a handful of connections and not fifty.
	slots   chan struct{}
	running sync.WaitGroup
	now     func() time.Time
}

// NewReads builds the reader. concurrency is how many pages are read at
// once across every import.
func NewReads(lifetime context.Context, concurrency int, pool *pgxpool.Pool, pages PageReader, papers Papers, indexing Indexing, originals Originals, logger *slog.Logger) (*Reads, error) {
	switch {
	case lifetime == nil:
		return nil, errors.New("notebookresearch: the reads' lifetime is not set")
	case concurrency < 1:
		return nil, errors.New("notebookresearch: the reads' concurrency must be at least 1")
	case pool == nil || pages == nil || papers == nil || indexing == nil || logger == nil:
		return nil, errors.New("notebookresearch: the reads need the pool, the page reader, the scholarly sources, the indexing queue and the logger")
	}
	if err := originals.check(); err != nil {
		return nil, err
	}
	return &Reads{pool: pool, pages: pages, papers: papers, indexing: indexing, originals: originals, logger: logger,
		lifetime: lifetime, slots: make(chan struct{}, concurrency), now: func() time.Time { return time.Now().UTC() }}, nil
}

// Start reads one imported document's page in the background. It is called
// after the import's transaction has committed.
func (r *Reads) Start(documentID int64, userID, requestID string) {
	r.running.Add(1)
	go func() {
		defer r.running.Done()
		select {
		case r.slots <- struct{}{}:
			defer func() { <-r.slots }()
			r.fill(documentID, userID, requestID)
		case <-r.lifetime.Done():
			r.markUnread(documentID, userID)
		}
	}()
}

// Stop waits for the reads under way to record how they ended. It is
// called at shutdown, after the lifetime context has been cancelled.
func (r *Reads) Stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		r.running.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("notebookresearch: some page reads had not finished recording when the server stopped")
	}
}

// imported is what the import wrote on the document for the read.
type imported struct {
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Paper   *Paper `json:"paper"`
}

func (r *Reads) fill(documentID int64, userID, requestID string) {
	ctx, done := context.WithTimeout(r.lifetime, readTimeout)
	defer done()
	claimed, err := New(r.pool).ClaimForReading(ctx, ClaimForReadingParams{Now: r.now(), ID: documentID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		// Deleted, or already taken by another read: nothing to do.
		return
	}
	if err != nil {
		r.logger.ErrorContext(ctx, "an imported page could not be taken for reading", "document_id", documentID, "error", err.Error())
		r.markUnread(documentID, userID)
		return
	}
	var about imported
	// Metadata this package wrote; if it cannot be read back there is no
	// address, and the document falls back to its title below.
	_ = json.Unmarshal(claimed.DocumentMetadata, &about)

	text := r.read(ctx, documentID, userID, about, claimed.Title)
	if r.lifetime.Err() != nil {
		// Cut by the server stopping, not by the page: do not record a
		// page as unreadable that was never given its chance.
		if text.original != nil {
			r.discard(ctx, documentID, text.original)
		}
		r.markUnread(documentID, userID)
		return
	}
	title := claimed.Title
	if text.title != "" && (strings.TrimSpace(title) == "" || title == about.URL) {
		// A pasted link is titled with its own address until the page
		// says what it is called.
		title = clip(text.title, maxTitleChars)
	}
	learned, _ := json.Marshal(text.learned)
	// Not the read's context: it may have run out on a slow page, and the
	// outcome still has to be written.
	record, finished := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer finished()
	if text.original != nil {
		recorded, err := r.recordPDF(record, documentID, userID, title, learned, text.original, r.now())
		if err != nil {
			r.logger.ErrorContext(ctx, "an imported PDF could not be recorded as the document's file", "document_id", documentID, "error", err.Error())
			r.markUnread(documentID, userID)
			return
		}
		if recorded {
			// The worker parses the file the moment it takes the job; a
			// queue that is down marks the document failed (indexqueue).
			_ = r.indexing.Enqueue(record, documentID, produce.LogContext{RequestID: requestID, UserID: userID})
		}
		return
	}
	sum := sha256.Sum256([]byte(text.markdown))
	stored, err := New(r.pool).StoreRead(record, StoreReadParams{
		Title: title, Markdown: text.markdown, ContentHash: hex.EncodeToString(sum[:]),
		Learned: learned, Now: r.now(), ID: documentID, UserID: userID,
	})
	if err != nil {
		r.logger.ErrorContext(ctx, "an imported page's text could not be stored", "document_id", documentID, "error", err.Error())
		r.markUnread(documentID, userID)
		return
	}
	if stored == 0 {
		// Deleted while it was being read.
		return
	}
	// The document is waiting for its job from here; a queue that is down
	// marks it failed with its own reason (indexqueue), and the error is
	// logged there.
	_ = r.indexing.Enqueue(record, documentID, produce.LogContext{RequestID: requestID, UserID: userID})
}

// readText is what a read came to.
type readText struct {
	markdown string
	// original is set when the address was a PDF, now stored: the file is
	// the document's text, parsed by the indexer, and markdown is empty.
	original *storedPDF
	title    string
	// learned is merged into the document's metadata: `fetched` always,
	// and where the text came from or why there is none.
	learned map[string]any
}

// read fetches the document's text: a paper's open full text when PubMed
// Central has it, otherwise what is at its address (addressToRead): a web
// page's text, or a PDF, which is stored for the indexer to parse. When
// none of that can be had, the text is the title and the search's
// snippet, and the document says so (`fetched: false` and the reason), so
// the source is usable and nobody is told it holds a page it does not.
func (r *Reads) read(ctx context.Context, documentID int64, userID string, about imported, title string) readText {
	problem := "The result has no address to read."
	if about.Paper != nil && about.Paper.PMCID != "" {
		body, err := r.papers.OpenFullText(ctx, about.Paper.PMCID, paperChars)
		if err == nil && body != "" {
			return readText{
				markdown: "# " + title + "\n\n" + body,
				learned:  map[string]any{"fetched": true, "read_from": "pubmed_central"},
			}
		}
		// Not open, or PMC did not answer: the paper's page is read
		// instead, which is what a paper outside PMC gets anyway.
	}
	if address := addressToRead(about); address != "" {
		var kept *storedPDF
		var storeErr error
		page := r.pages.Keep(ctx, address, r.originals.MaxBytes, func(ctx context.Context, pdf pagereader.PDF) error {
			kept, storeErr = r.originals.store(ctx, userID, documentID, pdf)
			return storeErr
		})
		switch {
		case page.Status == pagereader.Read && page.PDF && kept != nil:
			return readText{
				original: kept, title: kept.name,
				learned: map[string]any{
					"fetched": true, "read_from": "pdf", "read_url": page.URL,
					// What an upload records of its file, for the same views.
					"FILE_NAME": kept.name, "file_size": kept.size,
				},
			}
		case kept != nil:
			// Stored, and then the read failed after all: the file is
			// nobody's.
			r.discard(ctx, documentID, kept)
		case page.PDF && storeErr != nil:
			r.logger.WarnContext(ctx, "an imported PDF was not stored", "document_id", documentID, "error", storeErr.Error())
		}
		if page.Status == pagereader.Read && !page.PDF {
			learned := map[string]any{"fetched": true, "read_from": "page", "read_url": page.URL}
			if len(page.Notes) > 0 {
				// That the page was cut, or looks drawn by script.
				learned["read_notes"] = page.Notes
			}
			markdown := page.Text
			if !strings.HasPrefix(markdown, "# ") {
				heading := page.Title
				if heading == "" {
					heading = title
				}
				markdown = "# " + heading + "\n\n" + markdown
			}
			return readText{markdown: markdown, title: page.Title, learned: learned}
		}
		problem = page.Problem
		if problem == "" {
			problem = "The page has no readable text."
		}
	}
	parts := []string{"# " + title}
	if about.Snippet != "" {
		parts = append(parts, about.Snippet)
	}
	if about.URL != "" {
		parts = append(parts, about.URL)
	}
	return readText{
		markdown: strings.Join(parts, "\n\n"),
		learned:  map[string]any{"fetched": false, "fetch_problem": problem},
	}
}

// addressToRead is where an import's text is read from. An arXiv paper is
// read from its PDF, which is the paper: the address it is listed under is
// its abstract page, which holds the abstract the search already showed.
// arXiv serves every paper's PDF openly, at an address made from its id,
// and the newest version without one. Anything else is read at the
// address it was listed under.
func addressToRead(about imported) string {
	if about.Paper != nil && about.Paper.ArxivID != "" {
		return "https://arxiv.org/pdf/" + about.Paper.ArxivID
	}
	return about.URL
}

// markUnread records a read the server's stop cut short. It runs under its
// own short deadline because the lifetime is already over.
func (r *Reads) markUnread(documentID int64, userID string) {
	ctx, done := context.WithTimeout(context.Background(), recordTimeout)
	defer done()
	if _, err := New(r.pool).MarkUnread(ctx, MarkUnreadParams{Reason: interrupted, Now: r.now(), ID: documentID, UserID: userID}); err != nil {
		r.logger.ErrorContext(ctx, "an unread import could not be marked; importing it again after ten minutes reads it",
			"document_id", documentID, "error", err.Error())
	}
}
