package notebookresearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Imports is the import: how found results become a notebook's sources.
// It is the one path for both of its callers, the research box's two routes
// (importing.go) and the chat's add_sources tool (chat_sources.go), so a
// paper added either way is found again by the same identity, written in the
// same transaction, read by the same reader (reads.go, originals.go) and
// indexed by the same job.
type Imports struct {
	Pool  *pgxpool.Pool
	Reads *Reads
	// Now is the clock; nil is the system's.
	Now func() time.Time
}

// imports is the routes' import, over their own pool and reads.
func (r Routes) imports() Imports {
	return Imports{Pool: r.Pool, Reads: r.Reads, Now: r.Now}
}

func (i Imports) now() time.Time {
	if i.Now == nil {
		return time.Now().UTC()
	}
	return i.Now().UTC()
}

// Importing is who imports, into which notebook, and how a PDF among the
// results is parsed.
type Importing struct {
	UserID     string
	NotebookID int64
	// RequestID is carried into the reads' and the indexing's logs.
	RequestID string
	// Parser is the one a PDF among the items is read with, nil for the
	// local one; a document the account already holds keeps its own.
	Parser *string
}

// Imported is what became of one item.
type Imported struct {
	DocumentID int64
	// Title is the document's title as stored now.
	Title string
	// Created says the import made the document; otherwise it is one the
	// account already held, found by its address or its paper's ids.
	Created bool
	// WasSource says the document was already an enabled source of this
	// notebook before the import.
	WasSource bool
	// Reading says the document's text is not searchable yet: it waits for
	// its read (started by this import, or one already under way) or for
	// its indexing.
	Reading bool
}

var errNotebookAbsent = errors.New("notebook not found")

// Add links every item to the notebook in one transaction, making a
// document for each page the account does not hold yet, then starts the
// reads. It answers one Imported per item, in their order, or
// errNotebookAbsent when the notebook is not the account's, with nothing
// written.
//
// The reads run after the commit and are not waited for: reading a page or
// a paper takes seconds, a PDF minutes, and neither the route nor a chat
// turn should hold its caller that long. The document says where it is
// (`status`), and the chat's search finds it once it is indexed.
func (i Imports) Add(ctx context.Context, at Importing, items []item) ([]Imported, error) {
	now := i.now()
	type written struct {
		imported []Imported
		toRead   []int64
	}
	done, err := database.Write(ctx, i.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (written, error) {
		q := New(tx)
		// Marks the notebook used, and proves it is the caller's: every
		// row written below is written under this lock on it.
		touched, err := q.TouchNotebook(ctx, TouchNotebookParams{UsedAt: now, NotebookID: at.NotebookID, UserID: at.UserID})
		if err != nil {
			return written{}, err
		}
		if touched == 0 {
			return written{}, errNotebookAbsent
		}
		var out written
		for _, picked := range items {
			one, read, err := i.addOne(ctx, q, at, picked, now)
			if err != nil {
				return written{}, err
			}
			out.imported = append(out.imported, one)
			if read {
				out.toRead = append(out.toRead, one.DocumentID)
			}
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	// After the commit: a read takes the row the moment it starts.
	for _, documentID := range done.toRead {
		i.Reads.Start(documentID, at.UserID, at.RequestID)
	}
	return done.imported, nil
}

// addOne finds or makes one item's document and links it, inside the
// import's transaction. read says the document is to be read after the
// commit.
func (i Imports) addOne(ctx context.Context, q *Queries, at Importing, picked item, now time.Time) (Imported, bool, error) {
	lookup := ExistingImportParams{UserID: at.UserID, Identity: importIdentity(picked.address), Url: picked.url}
	if picked.paper != nil {
		lookup.Doi, lookup.Pmid, lookup.Pmcid, lookup.ArxivID = picked.paper.DOI, picked.paper.PMID, picked.paper.PMCID, picked.paper.ArxivID
	}
	documentID, err := q.ExistingImport(ctx, lookup)
	one := Imported{}
	read := false
	switch {
	case err == nil:
		// Held already: linked again, not read twice, unless its read
		// never ran.
		requeued, err := q.RequeueUnread(ctx, RequeueUnreadParams{Now: now, ID: documentID, UserID: at.UserID, StaleBefore: now.Add(-staleAfter)})
		if err != nil {
			return Imported{}, false, err
		}
		read = requeued == 1
	case errors.Is(err, pgx.ErrNoRows):
		metadata := map[string]any{"url": picked.url, "research_source": picked.source, "notebook_id": at.NotebookID}
		if picked.snippet != "" {
			metadata["snippet"] = picked.snippet
		}
		if picked.paper != nil {
			metadata["paper"] = picked.paper
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return Imported{}, false, err
		}
		sum := sha256.Sum256([]byte(picked.url))
		documentID, err = q.InsertPending(ctx, InsertPendingParams{
			UserID: at.UserID, Title: picked.title, Snippet: picked.snippet, ContentHash: hex.EncodeToString(sum[:]),
			Identity: lookup.Identity, Metadata: encoded, Parser: at.Parser, Now: now,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Another import of the same page committed between the look
			// and the insert: its document is the one.
			if documentID, err = q.ExistingImport(ctx, lookup); err != nil {
				return Imported{}, false, err
			}
		case err != nil:
			return Imported{}, false, err
		default:
			one.Created, read = true, true
		}
	default:
		return Imported{}, false, err
	}
	// Asked before the link, which switches a source back on: "already a
	// source" is about the notebook as the person last left it.
	if one.WasSource, err = q.EnabledSource(ctx, EnabledSourceParams{NotebookID: at.NotebookID, UserID: at.UserID, DocumentID: &documentID}); err != nil {
		return Imported{}, false, err
	}
	if err := q.LinkSource(ctx, LinkSourceParams{NotebookID: at.NotebookID, UserID: at.UserID, DocumentID: &documentID}); err != nil {
		return Imported{}, false, err
	}
	held, err := q.ImportedDocument(ctx, ImportedDocumentParams{ID: documentID, UserID: at.UserID})
	if err != nil {
		return Imported{}, false, err
	}
	one.DocumentID, one.Title = documentID, held.Title
	// Pending or processing is not searchable yet: being read, waiting for
	// its indexing job, or being indexed. A page that could not be read is
	// not left there: its title and snippet are stored and indexed instead
	// (reads.go), so it ends ready like any other.
	one.Reading = held.State == "pending" || held.State == "processing"
	return one, read, nil
}
