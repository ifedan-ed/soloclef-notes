package documentlibrary

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notes"
)

// addition is the body of an add.
type addition struct {
	Title     *string `json:"title"`
	Content   *string `json:"content"`
	SourceURL *string `json:"source_url"`
	FolderID  *int64  `json:"folder_id"`
}

// errFolderAbsent is the add naming a folder the caller does not have.
var errFolderAbsent = errors.New("documentlibrary: the folder is not the caller's")

// check reports every problem with the body at once, and returns the title
// and address as they will be stored.
func (a addition) check() (title string, source *string, failures []errorenvelope.Failure) {
	bad := func(field, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"body", field}, Message: message})
	}
	if a.Title != nil {
		title = strings.Join(strings.Fields(*a.Title), " ")
	}
	switch {
	case title == "":
		bad("title", "A title is required")
	case utf8.RuneCountInString(title) > maxTitleChars:
		bad("title", "The title should have at most "+strconv.Itoa(maxTitleChars)+" characters")
	}
	switch {
	case a.Content == nil || strings.TrimSpace(*a.Content) == "":
		bad("content", "The note's text is required")
	case len(*a.Content) > maxTextBytes:
		bad("content", "The text should be at most "+strconv.Itoa(maxTextBytes)+" bytes")
	case !utf8.ValidString(*a.Content) || strings.ContainsRune(*a.Content, 0):
		// Postgres refuses a NUL in text; say so here instead of failing.
		bad("content", "The text should be valid UTF-8 without NUL characters")
	}
	if a.SourceURL != nil && strings.TrimSpace(*a.SourceURL) != "" {
		address := strings.TrimSpace(*a.SourceURL)
		parsed, err := url.Parse(address)
		// Only an address a browser can open: the web app shows it as a
		// link, and a `javascript:` address must never become one.
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			utf8.RuneCountInString(address) > maxURLChars {
			bad("source_url", "The source should be an http or https address of at most "+strconv.Itoa(maxURLChars)+" characters")
		} else {
			source = &address
		}
	}
	if a.FolderID != nil && *a.FolderID < 1 {
		bad("folder_id", "Input should be a folder's id")
	}
	return title, source, failures
}

// add writes a note from a title and a text: the document and the `notes`
// row beside it, by the notes package's own statements, so a note added
// through the API is the same thing as one written in the app. It starts
// "pending" and its indexing is queued once the rows are committed, because
// the worker reads the row the moment it takes the message.
func (r Routes) add(x *exchange) {
	var asked addition
	if !x.body(&asked) {
		return
	}
	title, source, failures := asked.check()
	if len(failures) > 0 {
		x.invalid(failures...)
		return
	}
	text := *asked.Content
	id, err := database.Write(x.ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (int64, error) {
			if asked.FolderID != nil {
				owned, err := New(tx).FolderIsOwned(ctx, FolderIsOwnedParams{ID: *asked.FolderID, UserID: x.userID})
				if err != nil {
					return 0, err
				}
				if !owned {
					return 0, errFolderAbsent
				}
			}
			written := notes.New(tx)
			id, err := written.InsertNoteDocument(ctx, notes.InsertNoteDocumentParams{
				UserID: x.userID, Title: title, Markdown: text,
				ContentHash: notes.ContentHash(text), NeedsIndexing: true,
			})
			if err != nil {
				return 0, err
			}
			if err := written.InsertNoteRecord(ctx, notes.InsertNoteRecordParams{
				DocumentID: id, UserID: x.userID, Origin: notes.OriginHand,
			}); err != nil {
				return 0, err
			}
			if asked.FolderID == nil && source == nil {
				return id, nil
			}
			return id, New(tx).PlaceAddedNote(ctx, PlaceAddedNoteParams{
				FolderID: asked.FolderID, SourceUrl: source, ID: id, UserID: x.userID,
			})
		})
	if errors.Is(err, errFolderAbsent) {
		x.refuse(http.StatusNotFound, folderAbsent)
		return
	}
	if err != nil {
		x.fail("could not add the note", err)
		return
	}
	r.queueIndexing(x, id)

	row, err := New(r.Pool).DocumentOfOwner(x.ctx.Context(), DocumentOfOwnerParams{ID: id, UserID: x.userID})
	if err != nil {
		x.fail("could not read the note that was added", err)
		return
	}
	x.send(http.StatusCreated, fromRow(row))
}

// queueIndexing queues the document's indexing job after its write has
// committed. A queue that does not answer does not fail the request: the
// text is stored, and indexqueue has marked the document failed with a
// reason the app shows, from which a re-index starts it again.
func (r Routes) queueIndexing(x *exchange, documentID int64) {
	if err := r.Indexing.Enqueue(x.ctx.Context(), documentID, x.logContext()); err != nil {
		r.Logger.WarnContext(x.ctx.Context(), "a document was written but its indexing could not be queued",
			slog.String("request_id", x.requestID), slog.Int64("document_id", documentID),
			slog.String("error", err.Error()))
	}
}
