// Package notebookresearch serves a notebook's research box: finding new
// sources outside the account's documents and adding the ones a person
// picks.
//
//	GET  /api/v1/notebooks/{notebook_id}/research/sources    which sources this account can search
//	POST /api/v1/notebooks/{notebook_id}/research/search     one search, as server-sent events
//	POST /api/v1/notebooks/{notebook_id}/research/read-page  one pasted web page, added as a source
//	POST /api/v1/notebooks/{notebook_id}/research/import     picked results, added as sources
//
// # The sources
//
// The box starts on PubMed. Each source is itself: PubMed, arXiv, the
// bioRxiv and medRxiv preprint servers, and the two web search services an
// account may have connected with its own key (Exa, Tavily). A search of
// several asks each one separately and says, source by source, what it
// found or why it did not answer; the rows are then listed in turn, each
// naming the source that found it. Nothing here chooses a source for the
// reader or falls from one to another.
//
// The searches are the chat's own (soloclef_assistant/academic,
// soloclef_assistant/websearch), reached through the three small interfaces
// below, so the box and the chat cannot search a source two different ways.
// The page reader is the chat's too (soloclef_assistant/pagereader): it connects
// only to public addresses, on every redirect.
//
// Google Scholar is not searched by this tier. It has no API; the Python
// tier scraped it through a captcha solver that has not been ported, so it
// is reported as not available here and a search that names it is told so,
// by name. No other source stands in for it.
//
// # Importing
//
// A picked result becomes a `documents` row owned by the caller and a
// `notebook_sources` row, in one transaction, so the source is in the list
// the moment the route answers. Its page is then read by this process
// (reads.go) and the text is indexed by the indexing worker through the job
// every other document uses (internal/indexqueue). An address that is a
// PDF, by its bytes whatever the server calls it, is stored as the
// document's original file and parsed by that worker as an uploaded PDF is
// (originals.go): with the parser the import named in `parser`, else the
// local one. An arXiv paper is read from its PDF.
//
// # What it does better than the Python
//
//   - Every statement names the owner. A document found again by its
//     address or its paper ids is looked for among the caller's documents
//     only; the Python looked across a workspace.
//   - The import is one transaction with a unique identity per account, so
//     two imports of one page racing each other make one document, without
//     the savepoint-and-look-again the Python needed.
//   - A page that could not be read says so: the document records
//     `fetched: false` and why, in words, beside the title and snippet it
//     falls back to. The Python recorded only that it had not been read.
//   - An import whose read never ran (the server stopped) is read by
//     importing it again; under the Python it stayed pending for good.
//   - A pasted address is checked by the reader that connects to it, at
//     connection time and on each redirect, instead of by a DNS lookup
//     before the job was queued, which a redirect or a second lookup could
//     get past.
//   - A search asks no model and bills nothing, and a stream is not cut by
//     the server's write timeout while its reader keeps reading.
//   - Several sources are searched at once with one deadline each, and a
//     reader that goes away stops every search still running.
package notebookresearch

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// The paths.
const (
	SourcesPath  = "/api/v1/notebooks/{notebook_id}/research/sources"
	SearchPath   = "/api/v1/notebooks/{notebook_id}/research/search"
	ReadPagePath = "/api/v1/notebooks/{notebook_id}/research/read-page"
	ImportPath   = "/api/v1/notebooks/{notebook_id}/research/import"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + SourcesPath,
		http.MethodPost + " " + SearchPath,
		http.MethodPost + " " + ReadPagePath,
		http.MethodPost + " " + ImportPath,
	}
}

const notebookAbsent = "Notebook not found"

// Papers is the scholarly sources (*academic.Sources).
type Papers interface {
	PubMed(ctx context.Context, query string, limit int, fullText bool, sinceYear, untilYear int) ([]academic.Paper, error)
	Arxiv(ctx context.Context, query string, limit int) ([]academic.Paper, error)
	Preprints(ctx context.Context, query, server string, limit int) ([]academic.Paper, error)
	// OpenFullText is a paper's open-access body from PubMed Central.
	OpenFullText(ctx context.Context, pmcid string, limit int) (string, error)
}

// WebSearch is the account's own keyed web search (websearch.Search).
type WebSearch interface {
	Connected(ctx context.Context, userID string, provider websearch.Provider) (bool, error)
	Find(ctx context.Context, userID string, provider websearch.Provider, query string, limit int) ([]websearch.Result, error)
}

// PageReader reads public web pages (*pagereader.Reader).
type PageReader interface {
	// ReadWebPages reads pages side by side; a PDF is reported as one and
	// not downloaded. A deep search's quotes are read with it.
	ReadWebPages(ctx context.Context, addresses []string) []pagereader.Page
	// Keep reads one address; a PDF there is handed to keep to store, at
	// most maxBytes of it. An import's read is made with it.
	Keep(ctx context.Context, address string, maxBytes int64, keep pagereader.Keeper) pagereader.Page
}

// Indexing queues a document's indexing job (indexqueue.Indexing).
type Indexing interface {
	Enqueue(ctx context.Context, documentID int64, logContext produce.LogContext) error
}

// Routes is the four routes.
type Routes struct {
	Pool   *pgxpool.Pool
	Papers Papers
	Web    WebSearch
	// Pages reads the top results of a deep search, to quote the page
	// instead of the search service's teaser.
	Pages PageReader
	// Reads fills imported documents with their pages (NewReads).
	Reads *Reads
	// Parsers checks the parser an import names for a PDF it finds: one
	// of the four, configured on this server (internal/parserchoice).
	Parsers parserchoice.Registry
	// Stall is how long a search's reader may take nothing before the
	// stream is dropped: the server's write timeout, used as a limit on
	// stalling instead of on the whole response (internal/rollingdeadline).
	Stall  time.Duration
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
	// keepAlive and sourceTimeout are shortened by tests; zero is the
	// figures in search.go.
	keepAlive, sourceTimeout time.Duration
}

// Check reports what is missing. Called once at start-up, so a deployment
// wired without a piece is told then and not by the first search.
func (r Routes) Check() error {
	switch {
	case r.Pool == nil:
		return errors.New("notebookresearch: the database pool is not set")
	case r.Papers == nil:
		return errors.New("notebookresearch: the scholarly sources are not set")
	case r.Web == nil:
		return errors.New("notebookresearch: the web search is not set")
	case r.Pages == nil:
		return errors.New("notebookresearch: the page reader is not set")
	case r.Reads == nil:
		return errors.New("notebookresearch: the page reads are not set")
	case r.Parsers == nil:
		return errors.New("notebookresearch: the parser registry is not set")
	case r.Stall <= 0:
		return errors.New("notebookresearch: the stall timeout is not set")
	case r.Logger == nil:
		return errors.New("notebookresearch: the logger is not set")
	}
	return nil
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}
