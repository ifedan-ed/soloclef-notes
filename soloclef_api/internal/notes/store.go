package notes

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotebookRef names a notebook as a note's answer shows it.
type NotebookRef struct {
	ID    int64
	Title string
	Icon  *string
}

// Note is one note as the routes answer it. Text is set when one note is
// read and Preview when a list is: a list does not send every note's whole
// text.
type Note struct {
	ID           int64
	Title        string
	Text         string
	Preview      string
	Origin       string
	Notebook     *NotebookRef
	AutomationID *int64
	ThreadID     *int64
	MessageID    *int64
	// ChatNotebookID is the notebook whose chat a kept answer came from, or
	// nil (a plain chat, or one whose notebook is gone). Read with one note.
	ChatNotebookID *int64
	PinnedAt       *time.Time
	// State is the document's indexing state: "ready", "pending",
	// "processing" or "failed".
	State     string
	CreatedAt time.Time
	EditedAt  time.Time
	// Sources is every notebook the note is switched on as a source of.
	Sources []NotebookRef
}

// Page is one page of a list and the count of every note the filters match.
type Page struct {
	Notes  []Note
	Total  int64
	Offset int64
	Limit  int64
}

// HasMore reports whether a later page has notes.
func (p Page) HasMore() bool {
	return p.Offset+int64(len(p.Notes)) < p.Total
}

// The statements are free functions over a DBTX, so the same one runs on the
// pool or inside a transaction and the route says which by what it passes.
// Each takes the owner, which is always the signed-in caller.

// readPage reads a page and the notebooks each of its notes is a source of.
func readPage(ctx context.Context, db DBTX, userID string, filter Filter) (Page, error) {
	queries := New(db)
	rows, err := queries.ListNotes(ctx, listArguments(userID, filter))
	if err != nil {
		return Page{}, err
	}
	page := Page{Notes: make([]Note, 0, len(rows)), Offset: filter.Offset, Limit: filter.Limit}
	for _, row := range rows {
		page.Total = row.Total
		page.Notes = append(page.Notes, Note{
			ID: row.ID, Title: row.Title, Preview: row.Preview, Origin: row.Origin,
			Notebook: notebookRef(row.NotebookID, row.NotebookTitle, row.NotebookIcon),
			ThreadID: row.ThreadID, MessageID: row.MessageID, PinnedAt: row.PinnedAt,
			State: row.State, CreatedAt: row.CreatedAt, EditedAt: row.EditedAt,
		})
	}
	if len(rows) == 0 && filter.Offset > 0 {
		// A page past the end has no row to carry the window count, so the
		// first page is asked for its one row's count: the answer still
		// says how many there are.
		first := filter
		first.Offset, first.Limit = 0, 1
		counted, err := queries.ListNotes(ctx, listArguments(userID, first))
		if err != nil {
			return Page{}, err
		}
		if len(counted) > 0 {
			page.Total = counted[0].Total
		}
	}
	if err := attachSources(ctx, queries, userID, page.Notes); err != nil {
		return Page{}, err
	}
	return page, nil
}

// listArguments is the filter as the statement takes it. The search text is
// passed twice: as typed, for full text, and as a title pattern.
func listArguments(userID string, filter Filter) ListNotesParams {
	arguments := ListNotesParams{
		PreviewLength: previewLength,
		UserID:        userID,
		NotebookID:    filter.NotebookID,
		Unfiled:       filter.Unfiled,
		PinnedOnly:    filter.PinnedOnly,
		Origin:        filter.Origin,
		ThreadID:      filter.ThreadID,
		Sort:          filter.Sort,
		Skip:          filter.Offset,
		PageSize:      filter.Limit,
	}
	if filter.Query != "" {
		query, pattern := filter.Query, titlePattern(filter.Query)
		arguments.Query, arguments.TitlePattern = &query, &pattern
	}
	return arguments
}

// loadNote is the owner's note with its text and sources, or nil when the
// owner has no note by that id.
func loadNote(ctx context.Context, db DBTX, userID string, noteID int64) (*Note, error) {
	queries := New(db)
	row, err := queries.NoteOfOwner(ctx, NoteOfOwnerParams{ID: noteID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	notes := []Note{{
		ID: row.ID, Title: row.Title, Text: row.Markdown, Origin: row.Origin,
		Notebook:     notebookRef(row.NotebookID, row.NotebookTitle, row.NotebookIcon),
		AutomationID: row.AutomationID, ThreadID: row.ThreadID, MessageID: row.MessageID,
		ChatNotebookID: row.ChatNotebookID, PinnedAt: row.PinnedAt, State: row.State, CreatedAt: row.CreatedAt, EditedAt: row.EditedAt,
	}}
	if err := attachSources(ctx, queries, userID, notes); err != nil {
		return nil, err
	}
	return &notes[0], nil
}

// attachSources fills each note's Sources with one statement for the lot.
func attachSources(ctx context.Context, queries *Queries, userID string, notes []Note) error {
	if len(notes) == 0 {
		return nil
	}
	ids := make([]int64, len(notes))
	at := make(map[int64]int, len(notes))
	for index, note := range notes {
		ids[index] = note.ID
		at[note.ID] = index
		notes[index].Sources = []NotebookRef{}
	}
	rows, err := queries.SourcesOfNotes(ctx, SourcesOfNotesParams{UserID: userID, DocumentIds: ids})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.DocumentID == nil {
			continue
		}
		index := at[*row.DocumentID]
		notes[index].Sources = append(notes[index].Sources,
			NotebookRef{ID: row.NotebookID, Title: row.NotebookTitle, Icon: row.NotebookIcon})
	}
	return nil
}

// hasNotebook reports whether the owner has the notebook.
func hasNotebook(ctx context.Context, db DBTX, userID string, notebookID int64) (bool, error) {
	_, err := New(db).NotebookOfOwner(ctx, NotebookOfOwnerParams{ID: notebookID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func notebookRef(id *int64, title, icon *string) *NotebookRef {
	if id == nil {
		return nil
	}
	ref := &NotebookRef{ID: *id, Icon: icon}
	if title != nil {
		ref.Title = *title
	}
	return ref
}
