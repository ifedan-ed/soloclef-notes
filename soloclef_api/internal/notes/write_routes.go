package notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// errAbsent is a row the caller does not have, named by the request: the
// transaction is rolled back and the route answers its 404.
type errAbsent struct{ message string }

func (e errAbsent) Error() string { return e.message }

// create writes a note: by hand (no thread), or kept from a chat answer
// (thread_id and message_id). A kept answer is filed in the notebook whose
// chat it came from unless the request names another; a note written by
// hand is filed where the request says, or nowhere.
func (r Routes) create(x *exchange) {
	creation, failures, err := ReadCreation(x.ctx.Header("Content-Type"), requestbody.Buffered(x.ctx.Context()))
	if err != nil {
		x.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	if len(failures) > 0 {
		x.invalid(failures)
		return
	}
	if creation.OutOfRange {
		// An id too wide for a bigint names nothing the caller has.
		x.notFound(notebookOrChatAbsent(creation))
		return
	}

	title := NoteTitle(creation.Title, creation.Text)
	needsIndexing := creation.Text != ""
	userID := x.caller.UserID
	noteID, err := database.Write(x.ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (int64, error) {
			queries := New(tx)
			origin := OriginHand
			notebookID := creation.NotebookID
			if creation.ThreadID != nil {
				thread, err := queries.ThreadOfOwner(ctx, ThreadOfOwnerParams{ID: *creation.ThreadID, UserID: userID})
				if errors.Is(err, pgx.ErrNoRows) {
					return 0, errAbsent{threadAbsent}
				}
				if err != nil {
					return 0, err
				}
				if creation.MessageID != nil {
					_, err := queries.MessageInThread(ctx, MessageInThreadParams{ID: *creation.MessageID, ThreadID: thread.ID})
					if errors.Is(err, pgx.ErrNoRows) {
						return 0, errAbsent{messageAbsent}
					}
					if err != nil {
						return 0, err
					}
				}
				origin = OriginChat
				if notebookID == nil {
					notebookID = thread.NotebookID
				}
			}
			if notebookID != nil {
				owned, err := hasNotebook(ctx, tx, userID, *notebookID)
				if err != nil {
					return 0, err
				}
				if !owned {
					return 0, errAbsent{notebookAbsent}
				}
			}
			id, err := queries.InsertNoteDocument(ctx, InsertNoteDocumentParams{
				UserID: userID, Title: title, Markdown: creation.Text,
				ContentHash: contentHash(creation.Text), NeedsIndexing: needsIndexing,
			})
			if err != nil {
				return 0, err
			}
			return id, queries.InsertNoteRecord(ctx, InsertNoteRecordParams{
				DocumentID: id, UserID: userID, NotebookID: notebookID, Origin: origin,
				ThreadID: creation.ThreadID, MessageID: creation.MessageID,
			})
		})
	var absent errAbsent
	if errors.As(err, &absent) {
		x.notFound(absent.message)
		return
	}
	if err != nil {
		x.fail("could not write the note", err)
		return
	}
	if needsIndexing {
		r.queueIndexing(x, noteID)
	}
	x.sendNote(noteID, http.StatusCreated)
}

// update saves a note's title, its text, or both: what the editor's
// autosave sends. A changed text is indexed again, debounced (the package
// comment says how).
func (r Routes) update(x *exchange) {
	ids, ok := x.ids("note_id")
	if !ok {
		return
	}
	change, failures, err := ReadChange(x.ctx.Header("Content-Type"), requestbody.Buffered(x.ctx.Context()))
	if err != nil {
		x.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	if len(failures) > 0 {
		x.invalid(failures)
		return
	}
	if change.Title == nil && change.Text == nil {
		// Nothing to save is not an edit: the note's "last edited" stays.
		x.sendNote(ids[0], http.StatusOK)
		return
	}
	arguments := UpdateNoteTextParams{ID: ids[0], UserID: x.caller.UserID, Markdown: change.Text}
	if change.Text != nil {
		hash := contentHash(*change.Text)
		arguments.ContentHash = &hash
	}
	if change.Title != nil {
		// An emptied title field names the note from its text, as a new
		// note is named: the text being saved with it, or the one there.
		text := ""
		if strings.TrimSpace(*change.Title) == "" {
			saving, err := r.textForTitle(x, ids[0], change.Text)
			if err != nil {
				x.fail("could not read the note", err)
				return
			}
			if saving == nil {
				x.notFound(noteAbsent)
				return
			}
			text = *saving
		}
		named := NoteTitle(*change.Title, text)
		arguments.Title = &named
	}

	saved, err := New(r.Pool).UpdateNoteText(x.ctx.Context(), arguments)
	if errors.Is(err, pgx.ErrNoRows) {
		x.notFound(noteAbsent)
		return
	}
	if err != nil {
		x.fail("could not save the note", err)
		return
	}
	// A job already waiting reads the row when it runs, so it indexes this
	// text too; only a note with none waiting needs one.
	if saved.NeedsIndexing && saved.PreviousState != "pending" {
		r.queueIndexing(x, saved.ID)
	}
	x.sendNote(saved.ID, http.StatusOK)
}

// textForTitle is the text a note emptied of its title is named from: the
// text being saved with it, else the note's own. Nil when the caller has no
// such note.
func (r Routes) textForTitle(x *exchange, noteID int64, saving *string) (*string, error) {
	if saving != nil {
		return saving, nil
	}
	note, err := loadNote(x.ctx.Context(), r.Pool, x.caller.UserID, noteID)
	if err != nil || note == nil {
		return nil, err
	}
	return &note.Text, nil
}

// queueIndexing queues the note's delayed indexing job after its write has
// committed. A queue that does not answer does not fail the save: the text
// is stored, and indexqueue marks the note failed with a reason the app
// shows, so the next save queues the job again.
func (r Routes) queueIndexing(x *exchange, noteID int64) {
	if r.Indexing == nil {
		return
	}
	if err := r.Indexing.EnqueueAfter(x.ctx.Context(), noteID, r.indexDelay(), x.logContext()); err != nil {
		r.Logger.LogAttrs(x.ctx.Context(), slog.LevelWarn,
			"a note was saved but its indexing could not be queued",
			slog.String("request_id", x.requestID), slog.Int64("document_id", noteID),
			slog.String("error", err.Error()))
	}
}

// notebookOrChatAbsent is the 404 for a create naming an id too wide to be
// anything: the first of the ids it names.
func notebookOrChatAbsent(creation Creation) string {
	if creation.ThreadID == nil && creation.MessageID == nil {
		return notebookAbsent
	}
	return threadAbsent
}

// contentHash is the SHA-256 of the note's Markdown, the column every
// document carries for "is this text here already".
func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ContentHash is the hash a note's document is written with, for a package
// that adds a note through this package's statements (the document
// library's add): one definition, so a note is hashed the same whichever
// route wrote it.
func ContentHash(text string) string { return contentHash(text) }
