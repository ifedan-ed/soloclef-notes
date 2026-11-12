package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/fractionalindex"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notes"
)

// refusal is a change that was not made, in a sentence for the model to act
// on or pass on. Anything else that fails a change is the server's, and the
// model is told only that (tools.go).
type refusal struct{ message string }

func (r refusal) Error() string { return r.message }

func refuse(format string, args ...any) error { return refusal{fmt.Sprintf(format, args...)} }

// uniqueViolation is Postgres's code for a unique constraint refusing a row.
const uniqueViolation = "23505"

// violated reports whether err is the named unique constraint refusing a
// row. The constraint is named, and not only the code, so that some other
// key refusing a row is never reported as this sentence.
func violated(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraint
}

// nameTakenRace is two folders of one name under one parent, made by two
// writers at once after both asked whether the name was free.
func nameTakenRace(err error) bool { return violated(err, "uq_folders_owner_parent_name") }

// undoneTwiceRace is two undos of one action committing at once: the
// partial unique index on reverse_of lets one through.
func undoneTwiceRace(err error) bool { return violated(err, "uq_agent_action_log_reverse_of") }

// The tool names, which are also the action log's tool_name and so what an
// undo dispatches on. Exact names, never prefixes.
const (
	createNoteTool     = "create_note"
	editNoteTool       = "edit_note"
	renameDocumentTool = "rename_document"
	moveDocumentTool   = "move_document"
	deleteNoteTool     = "delete_note"
	createFolderTool   = "create_folder"
	renameFolderTool   = "rename_folder"
	moveFolderTool     = "move_folder"
	deleteFolderTool   = "delete_folder"
)

// lockedDocument is the document about to be changed, or a refusal naming the
// id the model gave.
func lockedDocument(ctx context.Context, q *Queries, who owner, id int64) (LockDocumentRow, error) {
	row, err := q.LockDocument(ctx, LockDocumentParams{ID: id, UserID: who.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, refuse("There is no document with id %d.", id)
	}
	return row, err
}

func lockedFolder(ctx context.Context, q *Queries, who owner, id int64) (LockFolderRow, error) {
	row, err := q.LockFolder(ctx, LockFolderParams{ID: id, UserID: who.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, refuse("There is no folder with id %d.", id)
	}
	return row, err
}

// requireFolder refuses a folder id that is not the account's. None is the
// top level and always exists.
func requireFolder(ctx context.Context, q *Queries, who owner, id *int64) error {
	if id == nil {
		return nil
	}
	exists, err := q.FolderExists(ctx, FolderExistsParams{ID: *id, UserID: who.userID})
	if err != nil {
		return err
	}
	if !exists {
		return refuse("There is no folder with id %d.", *id)
	}
	return nil
}

// noteOnly refuses a change that only a note's text can take.
func noteOnly(row LockDocumentRow, doing string) error {
	if row.IsNote {
		return nil
	}
	return refuse("%q is %s, not a note. Its text comes from its source, so it is not %s from the chat.", row.Title, aKind(row.DocumentType), doing)
}

func stateOf(row LockDocumentRow) *documentState {
	return &documentState{ID: row.ID, Title: row.Title, FolderID: row.FolderID, TextMD5: row.TextMd5}
}

// ---------------------------------------------------------------------------
// Notes and documents
// ---------------------------------------------------------------------------

func (c Changes) createNote(ctx context.Context, who owner, call tool.Call, title, text string, folderID *int64) (string, error) {
	title = notes.NoteTitle(title, text)
	type made struct {
		id   int64
		path string
		log  logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (made, error) {
		q := New(tx)
		if err := requireFolder(ctx, q, who, folderID); err != nil {
			return made{}, err
		}
		id, err := q.InsertNote(ctx, InsertNoteParams{
			UserID: who.userID, Title: title, Markdown: text, ContentHash: notes.ContentHash(text),
			NeedsIndexing: text != "", FolderID: folderID,
		})
		if err != nil {
			return made{}, err
		}
		// Kept from this conversation: the note links back to it, and in a
		// notebook's chat it is filed in the notebook, as a note saved from
		// a chat answer is.
		if err := q.InsertNoteRecord(ctx, InsertNoteRecordParams{
			DocumentID: id, UserID: who.userID, Origin: notes.OriginChat, ThreadID: &who.threadID,
			NotebookID: who.notebookID,
		}); err != nil {
			return made{}, err
		}
		// Undoing a creation is deleting the note, so there is no
		// before-state but the fact that it did not exist.
		l, err := record(ctx, tx, who, change{
			tool: createNoteTool, call: call, item: documentItem, itemID: id,
			after:    descriptor{Document: &documentState{ID: id, Title: title, FolderID: folderID, TextMD5: textMD5(text)}},
			document: &documentBefore{documentID: &id},
		})
		if err != nil {
			return made{}, err
		}
		folderTree, err := readTree(ctx, q, who.userID)
		return made{id: id, path: folderTree.path(folderID), log: l}, err
	})
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	answer := fmt.Sprintf("Created the note %q (document %d) in %s.", title, result.id, result.path)
	if text != "" {
		answer += c.index(ctx, who, result.id)
	}
	return answer, nil
}

// noteEdit is an edit of a note's text: the whole text replaced, or one
// passage of it.
type noteEdit struct {
	content          *string
	oldText, newText string
}

func (e noteEdit) apply(text string) (string, error) {
	if e.content != nil {
		return *e.content, nil
	}
	switch count := strings.Count(text, e.oldText); count {
	case 1:
		return strings.Replace(text, e.oldText, e.newText, 1), nil
	case 0:
		return "", refuse("The text to replace is not in the note. Read the note again and copy the passage exactly.")
	default:
		return "", refuse("The text to replace appears %d times in the note. Include more of the text around it, so that it appears once.", count)
	}
}

func (c Changes) editNote(ctx context.Context, who owner, call tool.Call, id int64, edit noteEdit) (string, error) {
	type edited struct {
		title string
		log   logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (edited, error) {
		q := New(tx)
		row, err := lockedDocument(ctx, q, who, id)
		if err != nil {
			return edited{}, err
		}
		// An edit is of the text, which in a notebook's chat is held to
		// what that chat may read (mayRead).
		if err := mayRead(ctx, q, who, id); err != nil {
			return edited{}, err
		}
		if err := noteOnly(row, "edited"); err != nil {
			return edited{}, err
		}
		text, err := edit.apply(row.Markdown)
		if err != nil {
			return edited{}, err
		}
		if text == row.Markdown {
			return edited{}, refuse("That leaves the note exactly as it is; nothing was changed.")
		}
		if len(text) > notes.MaxTextBytes {
			return edited{}, refuse("The note would be longer than a note may be (%d bytes).", notes.MaxTextBytes)
		}
		if err := q.SetNoteText(ctx, SetNoteTextParams{Markdown: text, ContentHash: notes.ContentHash(text), ID: id, UserID: who.userID}); err != nil {
			return edited{}, err
		}
		after := stateOf(row)
		after.TextMD5 = textMD5(text)
		l, err := record(ctx, tx, who, change{
			tool: editNoteTool, call: call, item: documentItem, itemID: id, after: descriptor{Document: after},
			document: &documentBefore{documentID: &id, content: &row.Markdown},
		})
		return edited{title: row.Title, log: l}, err
	})
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Edited the note %q (document %d).", result.title, id) + c.index(ctx, who, id), nil
}

// renameDocument changes a title, for a document of any kind: the title is
// the row's, and search reads it from the row, so nothing is re-indexed.
func (c Changes) renameDocument(ctx context.Context, who owner, call tool.Call, id int64, title string) (string, error) {
	type renamed struct {
		before string
		log    logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (renamed, error) {
		q := New(tx)
		row, err := lockedDocument(ctx, q, who, id)
		if err != nil {
			return renamed{}, err
		}
		if row.Title == title {
			return renamed{}, refuse("The document is already called %q.", title)
		}
		if err := q.SetDocumentTitle(ctx, SetDocumentTitleParams{Title: title, ID: id, UserID: who.userID}); err != nil {
			return renamed{}, err
		}
		after := stateOf(row)
		after.Title = title
		l, err := record(ctx, tx, who, change{
			tool: renameDocumentTool, call: call, item: documentItem, itemID: id, after: descriptor{Document: after},
			document: &documentBefore{documentID: &id, title: &row.Title},
		})
		return renamed{before: row.Title, log: l}, err
	})
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Renamed %q to %q (document %d).", result.before, title, id), nil
}

// moveDocument files a document of any kind in a folder, or in none. Search
// reads a document's folder from its row, so nothing is re-indexed.
func (c Changes) moveDocument(ctx context.Context, who owner, call tool.Call, id int64, folderID *int64) (string, error) {
	type moved struct {
		title, from, to string
		log             logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (moved, error) {
		q := New(tx)
		row, err := lockedDocument(ctx, q, who, id)
		if err != nil {
			return moved{}, err
		}
		if err := requireFolder(ctx, q, who, folderID); err != nil {
			return moved{}, err
		}
		folderTree, err := readTree(ctx, q, who.userID)
		if err != nil {
			return moved{}, err
		}
		if sameID(row.FolderID, folderID) {
			return moved{}, refuse("%q is already in %s.", row.Title, folderTree.path(folderID))
		}
		if err := q.SetDocumentFolder(ctx, SetDocumentFolderParams{FolderID: folderID, ID: id, UserID: who.userID}); err != nil {
			return moved{}, err
		}
		after := stateOf(row)
		after.FolderID = folderID
		l, err := record(ctx, tx, who, change{
			tool: moveDocumentTool, call: call, item: documentItem, itemID: id, after: descriptor{Document: after},
			document: &documentBefore{documentID: &id, folderID: row.FolderID},
		})
		return moved{title: row.Title, from: folderTree.path(row.FolderID), to: folderTree.path(folderID), log: l}, err
	})
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Moved %q from %s to %s.", result.title, result.from, result.to), nil
}

// deleteNote deletes a note and records all of it, in one transaction. The
// row goes in the transaction (its passages cascade with it), so the note
// is gone from every list and search the moment it commits, and the points
// it had in the vector store are removed after.
func (c Changes) deleteNote(ctx context.Context, who owner, call tool.Call, id int64) (string, error) {
	type deleted struct {
		title  string
		chunks []int64
		log    logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (deleted, error) {
		q := New(tx)
		row, err := lockedDocument(ctx, q, who, id)
		if err != nil {
			return deleted{}, err
		}
		if err := noteOnly(row, "deleted"); err != nil {
			return deleted{}, err
		}
		if row.HasFiles {
			return deleted{}, refuse("%q has stored files, which an undo could not bring back, so it is not deleted from the chat.", row.Title)
		}
		sources, err := q.DocumentNotebookSources(ctx, DocumentNotebookSourcesParams{DocumentID: id, UserID: who.userID})
		if err != nil {
			return deleted{}, err
		}
		kept := deletedNote{
			DocumentMetadata: json.RawMessage(row.DocumentMetadata),
			Note: noteRecord{
				NotebookID: row.NotebookID, AutomationID: row.AutomationID, ThreadID: row.ThreadID,
				MessageID: row.MessageID, PinnedAt: row.PinnedAt,
			},
		}
		if row.Origin != nil {
			kept.Note.Origin = *row.Origin
		}
		for _, source := range sources {
			kept.Sources = append(kept.Sources, notebookSource{NotebookID: source.NotebookID, Enabled: source.Enabled})
		}
		metadata, err := json.Marshal(kept)
		if err != nil {
			return deleted{}, err
		}
		chunks, err := q.ChunkIDsOfDocument(ctx, ChunkIDsOfDocumentParams{DocumentID: id, UserID: who.userID})
		if err != nil {
			return deleted{}, err
		}
		// The revision first: its document_id is cleared by the delete
		// (ON DELETE SET NULL), and the action row keeps the id.
		l, err := record(ctx, tx, who, change{
			tool: deleteNoteTool, call: call, item: documentItem, itemID: id,
			document: &documentBefore{
				documentID: &id, content: &row.Markdown, title: &row.Title, folderID: row.FolderID, metadata: metadata,
			},
		})
		if err != nil {
			return deleted{}, err
		}
		return deleted{title: row.Title, chunks: chunks, log: l}, q.DeleteDocument(ctx, DeleteDocumentParams{ID: id, UserID: who.userID})
	})
	if err != nil {
		return "", err
	}
	c.forgetPassages(ctx, result.chunks)
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Deleted the note %q.", result.title), nil
}

// ---------------------------------------------------------------------------
// Folders
// ---------------------------------------------------------------------------

// placeFolder checks that a folder of this name, with `below` levels under
// it, may go under parent, and returns its position after the parent's last
// folder. except is the folder itself, when it is being moved or renamed.
func placeFolder(ctx context.Context, q *Queries, who owner, parent *int64, name string, except int64, below int64) (string, error) {
	parentDepth := int64(0)
	if parent != nil {
		depth, err := q.FolderDepth(ctx, FolderDepthParams{ID: *parent, UserID: who.userID})
		if err != nil {
			return "", err
		}
		parentDepth = depth
	}
	if total := parentDepth + 1 + below; total > folders.MaxDepth {
		return "", refuse("Folders may be nested %d deep, and this would make %d.", folders.MaxDepth, total)
	}
	taken, err := q.SiblingNamed(ctx, SiblingNamedParams{UserID: who.userID, Name: name, ExceptID: except, ParentID: parent})
	if err != nil {
		return "", err
	}
	if taken {
		return "", refuse("There is already a folder called %q there.", name)
	}
	last, err := q.LastSiblingPosition(ctx, LastSiblingPositionParams{UserID: who.userID, ParentID: parent})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fractionalindex.KeyBetween(nil, nil)
	case err != nil:
		return "", err
	}
	return fractionalindex.KeyBetween(&last, nil)
}

func (c Changes) createFolder(ctx context.Context, who owner, call tool.Call, name string, parent *int64) (string, error) {
	type made struct {
		id   int64
		path string
		log  logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (made, error) {
		q := New(tx)
		if err := requireFolder(ctx, q, who, parent); err != nil {
			return made{}, err
		}
		position, err := placeFolder(ctx, q, who, parent, name, 0, 0)
		if err != nil {
			return made{}, err
		}
		id, err := q.InsertFolder(ctx, InsertFolderParams{UserID: who.userID, Name: name, Position: position, ParentID: parent})
		if err != nil {
			return made{}, err
		}
		l, err := record(ctx, tx, who, change{
			tool: createFolderTool, call: call, item: folderItem, itemID: id,
			after:  descriptor{Folder: &folderState{ID: id, Name: name, ParentID: parent}},
			folder: &folderBefore{folderID: &id},
		})
		if err != nil {
			return made{}, err
		}
		folderTree, err := readTree(ctx, q, who.userID)
		return made{id: id, path: folderTree.path(&id), log: l}, err
	})
	if nameTakenRace(err) {
		return "", refuse("There is already a folder called %q there.", name)
	}
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Created the folder %s (folder %d).", result.path, result.id), nil
}

func (c Changes) renameFolder(ctx context.Context, who owner, call tool.Call, id int64, name string) (string, error) {
	type renamed struct {
		before string
		log    logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (renamed, error) {
		q := New(tx)
		row, err := lockedFolder(ctx, q, who, id)
		if err != nil {
			return renamed{}, err
		}
		if row.Name == name {
			return renamed{}, refuse("The folder is already called %q.", name)
		}
		taken, err := q.SiblingNamed(ctx, SiblingNamedParams{UserID: who.userID, Name: name, ExceptID: id, ParentID: row.ParentID})
		if err != nil {
			return renamed{}, err
		}
		if taken {
			return renamed{}, refuse("There is already a folder called %q there.", name)
		}
		if err := q.SetFolderName(ctx, SetFolderNameParams{Name: name, ID: id, UserID: who.userID}); err != nil {
			return renamed{}, err
		}
		l, err := record(ctx, tx, who, change{
			tool: renameFolderTool, call: call, item: folderItem, itemID: id,
			after:  descriptor{Folder: &folderState{ID: id, Name: name, ParentID: row.ParentID}},
			folder: &folderBefore{folderID: &id, name: &row.Name},
		})
		return renamed{before: row.Name, log: l}, err
	})
	if nameTakenRace(err) {
		return "", refuse("There is already a folder called %q there.", name)
	}
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Renamed the folder %q to %q.", result.before, name), nil
}

func (c Changes) moveFolder(ctx context.Context, who owner, call tool.Call, id int64, parent *int64) (string, error) {
	type moved struct {
		name, from, to string
		log            logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (moved, error) {
		q := New(tx)
		row, err := lockedFolder(ctx, q, who, id)
		if err != nil {
			return moved{}, err
		}
		if err := requireFolder(ctx, q, who, parent); err != nil {
			return moved{}, err
		}
		if sameID(row.ParentID, parent) {
			return moved{}, refuse("The folder %q is already there.", row.Name)
		}
		if parent != nil {
			inside, err := q.IsWithin(ctx, IsWithinParams{Ancestor: id, ID: *parent, UserID: who.userID})
			if err != nil {
				return moved{}, err
			}
			if inside {
				return moved{}, refuse("A folder cannot be moved into itself or into a folder inside it.")
			}
		}
		below, err := q.SubtreeDepth(ctx, SubtreeDepthParams{ID: id, UserID: who.userID})
		if err != nil {
			return moved{}, err
		}
		position, err := placeFolder(ctx, q, who, parent, row.Name, id, below)
		if err != nil {
			return moved{}, err
		}
		folderTree, err := readTree(ctx, q, who.userID)
		if err != nil {
			return moved{}, err
		}
		if err := q.SetFolderParent(ctx, SetFolderParentParams{ParentID: parent, Position: position, ID: id, UserID: who.userID}); err != nil {
			return moved{}, err
		}
		l, err := record(ctx, tx, who, change{
			tool: moveFolderTool, call: call, item: folderItem, itemID: id,
			after:  descriptor{Folder: &folderState{ID: id, Name: row.Name, ParentID: parent}},
			folder: &folderBefore{folderID: &id, parentID: row.ParentID, position: &row.Position},
		})
		return moved{name: row.Name, from: folderTree.path(row.ParentID), to: folderTree.path(parent), log: l}, err
	})
	if nameTakenRace(err) {
		return "", refuse("There is already a folder with that name there.")
	}
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Moved the folder %q from %s to %s.", result.name, result.from, result.to), nil
}

// deleteFolder deletes an empty folder. One with anything in it is refused:
// what a delete of a full folder removes is everything below it, and an
// undo that is "the folder again" must not be a guess at all of that.
func (c Changes) deleteFolder(ctx context.Context, who owner, call tool.Call, id int64) (string, error) {
	type deleted struct {
		path string
		log  logged
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (deleted, error) {
		q := New(tx)
		row, err := lockedFolder(ctx, q, who, id)
		if err != nil {
			return deleted{}, err
		}
		empty, err := q.FolderIsEmpty(ctx, FolderIsEmptyParams{ID: id, UserID: who.userID})
		if err != nil {
			return deleted{}, err
		}
		if !empty {
			return deleted{}, refuse("The folder %q is not empty. Only an empty folder is deleted from the chat: move or delete what is in it first.", row.Name)
		}
		sources, err := q.FolderNotebookSources(ctx, FolderNotebookSourcesParams{FolderID: id, UserID: who.userID})
		if err != nil {
			return deleted{}, err
		}
		var removed descriptor
		for _, source := range sources {
			removed.FolderSources = append(removed.FolderSources, notebookSource{NotebookID: source.NotebookID, Enabled: source.Enabled})
		}
		folderTree, err := readTree(ctx, q, who.userID)
		if err != nil {
			return deleted{}, err
		}
		l, err := record(ctx, tx, who, change{
			tool: deleteFolderTool, call: call, item: folderItem, itemID: id, after: removed,
			folder: &folderBefore{folderID: &id, name: &row.Name, parentID: row.ParentID, position: &row.Position},
		})
		if err != nil {
			return deleted{}, err
		}
		return deleted{path: folderTree.path(&id), log: l}, q.DeleteFolder(ctx, DeleteFolderParams{ID: id, UserID: who.userID})
	})
	if err != nil {
		return "", err
	}
	c.announce(ctx, call, result.log)
	return fmt.Sprintf("Deleted the empty folder %s.", result.path), nil
}
