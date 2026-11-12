package documentlibrary

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documents"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notes"
)

// replacement is the body of a save.
type replacement struct {
	SourceMarkdown *string `json:"source_markdown"`
	Title          *string `json:"title"`
}

// saved is what a save answers.
type saved struct {
	Status     string     `json:"status"`
	DocumentID int64      `json:"document_id"`
	Message    string     `json:"message"`
	UpdatedAt  *time.Time `json:"updated_at"`
}

// refusal is a save that stopped inside its transaction with an answer.
type refusal struct {
	status  int
	message string
}

func (r refusal) Error() string { return r.message }

// outcome is what the transaction decided.
type outcome struct {
	updatedAt *time.Time
	// queue says an indexing job is owed: the text changed and no job is
	// already waiting to read the row.
	queue     bool
	unchanged bool
}

// save replaces a document's text, and its title when one is given.
//
// The row is read under a lock, its current text is kept as a version by the
// restore's own steps (documents.ReplaceText), and the new text is written
// and marked pending, all in one transaction. The indexing job is queued
// after the commit. A document that was already "pending" has a job waiting
// that will read this text when it runs, so no second one is queued.
func (r Routes) save(x *exchange) {
	id, ok := x.documentID()
	if !ok {
		return
	}
	var asked replacement
	if !x.body(&asked) {
		return
	}
	var failures []errorenvelope.Failure
	bad := func(field, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"body", field}, Message: message})
	}
	switch {
	case asked.SourceMarkdown == nil:
		bad("source_markdown", "Field required")
	case len(*asked.SourceMarkdown) > maxTextBytes:
		bad("source_markdown", "The text should be at most "+strconv.Itoa(maxTextBytes)+" bytes")
	case !utf8.ValidString(*asked.SourceMarkdown) || strings.ContainsRune(*asked.SourceMarkdown, 0):
		bad("source_markdown", "The text should be valid UTF-8 without NUL characters")
	}
	var title *string
	if asked.Title != nil {
		// A title that is given must be one; leaving it out keeps the
		// document's.
		given := strings.Join(strings.Fields(*asked.Title), " ")
		if given == "" || utf8.RuneCountInString(given) > maxTitleChars {
			bad("title", "A title should have 1 to "+strconv.Itoa(maxTitleChars)+" characters; leave it out to keep the document's")
		}
		title = &given
	}
	if len(failures) > 0 {
		x.invalid(failures...)
		return
	}
	text := *asked.SourceMarkdown
	now := r.now()

	done, err := database.Write(x.ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			queries := New(tx)
			current, err := queries.DocumentForSave(ctx, DocumentForSaveParams{ID: id, UserID: x.userID})
			if errors.Is(err, pgx.ErrNoRows) {
				return outcome{}, refusal{http.StatusNotFound, absent(strconv.FormatInt(id, 10))}
			}
			if err != nil {
				return outcome{}, err
			}
			switch {
			case current.State == "deleting":
				// Gone as far as a person can tell; a save must not write
				// over the removal.
				return outcome{}, refusal{http.StatusNotFound, absent(strconv.FormatInt(id, 10))}
			case current.DocumentType == "ARTIFACT":
				return outcome{}, refusal{http.StatusConflict, artifactReadOnly}
			}
			newTitle := current.Title
			if title != nil {
				newTitle = *title
			}
			textChanged := current.SourceMarkdown == nil || *current.SourceMarkdown != text
			if !textChanged && newTitle == current.Title {
				return outcome{unchanged: true}, nil
			}
			if err := documents.ReplaceText(ctx, tx, documents.RestoreWork{
				Document: documents.Restorable{
					ID: current.ID, UserID: x.userID, Title: current.Title,
					ContentHash: current.ContentHash, SourceMarkdown: current.SourceMarkdown,
				},
				Version: documents.Version{SourceMarkdown: &text, Title: &newTitle},
				Now:     now,
			}); err != nil {
				return outcome{}, err
			}
			updatedAt, err := queries.MarkSaved(ctx, MarkSavedParams{
				ContentHash: notes.ContentHash(text), Now: now, TextChanged: textChanged, ID: id, UserID: x.userID,
			})
			if err != nil {
				return outcome{}, err
			}
			return outcome{updatedAt: utc(updatedAt), queue: textChanged && current.State != "pending"}, nil
		})
	var refused refusal
	if errors.As(err, &refused) {
		x.refuse(refused.status, refused.message)
		return
	}
	if err != nil {
		x.fail("could not save the document", err)
		return
	}
	if done.queue {
		r.queueIndexing(x, id)
	}
	message := savedMessage
	if done.unchanged {
		message = unchangedMessage
	}
	x.send(http.StatusOK, saved{Status: "saved", DocumentID: id, Message: message, UpdatedAt: done.updatedAt})
}
