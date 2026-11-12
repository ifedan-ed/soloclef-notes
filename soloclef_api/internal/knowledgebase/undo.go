package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notes"
)

// outcome is how one undo ended.
type outcome int

const (
	undone outcome = iota + 1
	// alreadyUndone: an undo of this action exists.
	alreadyUndone
	// notUndoable: the action is not one this package can take back (it is
	// itself an undo, or another tool's), or its item has changed since,
	// or taking it back would collide with something made since.
	notUndoable
	// absent: no such action in the owner's thread.
	absent
)

// undoResult is an undo's outcome, the sentence that says it, and the id
// of the action row that records the undo.
type undoResult struct {
	outcome   outcome
	message   string
	newAction int64
}

// after is what an undo owes once its transaction has committed.
type after struct {
	index  []int64
	forget []int64
}

// Undo takes back one action in the owner's thread. A refusal is an
// outcome, not an error; an error is the server's.
func (c Changes) undo(ctx context.Context, who owner, actionID int64) (undoResult, error) {
	type done struct {
		result undoResult
		owed   after
	}
	result, err := database.Write(ctx, c.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (done, error) {
		q := New(tx)
		action, err := q.LockActionToUndo(ctx, LockActionToUndoParams{UserID: who.userID, ID: actionID, ThreadID: who.threadID})
		if errors.Is(err, pgx.ErrNoRows) {
			return done{result: undoResult{outcome: absent, message: "Action not found."}}, nil
		}
		if err != nil {
			return done{}, err
		}
		switch {
		case action.UndoneBy != 0:
			return done{result: undoResult{outcome: alreadyUndone, message: "This change has already been undone.", newAction: action.UndoneBy}}, nil
		case action.ReverseOf != nil:
			return done{result: undoResult{outcome: notUndoable, message: "This is itself an undo; ask the assistant to make the change again instead."}}, nil
		case !action.Reversible:
			return done{result: undoResult{outcome: notUndoable, message: "This action cannot be undone."}}, nil
		}
		var state descriptor
		if len(action.ReverseDescriptor) > 0 {
			if err := json.Unmarshal(action.ReverseDescriptor, &state); err != nil {
				return done{}, fmt.Errorf("reading action %d's after-state: %w", actionID, err)
			}
		}
		u := undoer{q: q, who: who, action: action, state: state}
		message, owed, err := u.run(ctx)
		if nameTakenRace(err) {
			// A folder of the name was made beside it between the check
			// and the write.
			return done{}, refuse("A folder of that name has just been made there, so this cannot be undone.")
		}
		if err != nil {
			// A refusal included: returning it rolls the transaction back,
			// so a refusal leaves every row as it was.
			return done{}, err
		}
		// The undo is itself recorded: a row pointing at the action, which
		// is what marks the action undone (the unique index on reverse_of
		// makes a second one impossible).
		args, _ := json.Marshal(map[string]int64{"reverted_action_id": actionID})
		row, err := q.InsertAction(ctx, InsertActionParams{
			ThreadID: who.threadID, UserID: who.userID, ToolName: action.ToolName, Args: args,
			Reversible: false, ReverseOf: &actionID,
		})
		if undoneTwiceRace(err) {
			return done{}, errAlreadyUndone
		}
		if err != nil {
			return done{}, err
		}
		return done{result: undoResult{outcome: undone, message: message, newAction: row.ID}, owed: owed}, nil
	})
	var refused refusal
	switch {
	case errors.As(err, &refused):
		return undoResult{outcome: notUndoable, message: refused.message}, nil
	case errors.Is(err, errAlreadyUndone):
		return undoResult{outcome: alreadyUndone, message: "This change has already been undone."}, nil
	case err != nil:
		return undoResult{}, err
	}
	for _, id := range result.owed.index {
		if trouble := c.index(ctx, who, id); trouble != "" {
			result.result.message += trouble
		}
	}
	c.forgetPassages(ctx, result.owed.forget)
	return result.result, nil
}

// errAlreadyUndone rolls back an undo that lost a race with another undo of
// the same action: the other one's row is the record.
var errAlreadyUndone = errors.New("knowledgebase: the action was undone by another request")

// undoer takes back one action inside the undo's transaction.
type undoer struct {
	q      *Queries
	who    owner
	action LockActionToUndoRow
	state  descriptor
}

// run dispatches on the exact tool name. A prefix would send rename_folder
// where rename_document goes, and delete_folder where delete_note goes.
func (u undoer) run(ctx context.Context) (string, after, error) {
	switch u.action.ToolName {
	case createNoteTool:
		return u.uncreateNote(ctx)
	case editNoteTool:
		return u.unedit(ctx)
	case renameDocumentTool:
		return u.unrenameDocument(ctx)
	case moveDocumentTool:
		return u.unmoveDocument(ctx)
	case deleteNoteTool:
		return u.undeleteNote(ctx)
	case createFolderTool:
		return u.uncreateFolder(ctx)
	case renameFolderTool:
		return u.unrenameFolder(ctx)
	case moveFolderTool:
		return u.unmoveFolder(ctx)
	case deleteFolderTool:
		return u.undeleteFolder(ctx)
	}
	return "", after{}, refuse("This action cannot be undone.")
}

func (u undoer) documentRevision(ctx context.Context) (DocumentRevisionOfRow, error) {
	revision, err := u.q.DocumentRevisionOf(ctx, DocumentRevisionOfParams{ActionID: u.action.ID, UserID: u.who.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return revision, refuse("There is no record of how this was before, so it cannot be undone.")
	}
	return revision, err
}

func (u undoer) folderRevision(ctx context.Context) (FolderRevisionOfRow, error) {
	revision, err := u.q.FolderRevisionOf(ctx, FolderRevisionOfParams{ActionID: u.action.ID, UserID: u.who.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return revision, refuse("There is no record of how this was before, so it cannot be undone.")
	}
	return revision, err
}

// currentDocument is the document the change left, locked, or a refusal
// when it is gone.
func (u undoer) currentDocument(ctx context.Context) (LockDocumentRow, *documentState, error) {
	if u.state.Document == nil {
		return LockDocumentRow{}, nil, refuse("There is no record of what this change left, so it cannot be undone.")
	}
	row, err := u.q.LockDocument(ctx, LockDocumentParams{ID: u.state.Document.ID, UserID: u.who.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, nil, refuse("The document has been deleted since, so there is nothing to undo.")
	}
	return row, u.state.Document, err
}

func (u undoer) currentFolder(ctx context.Context) (LockFolderRow, *folderState, error) {
	if u.state.Folder == nil {
		return LockFolderRow{}, nil, refuse("There is no record of what this change left, so it cannot be undone.")
	}
	row, err := u.q.LockFolder(ctx, LockFolderParams{ID: u.state.Folder.ID, UserID: u.who.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, nil, refuse("The folder has been deleted since, so there is nothing to undo.")
	}
	return row, u.state.Folder, err
}

// uncreateNote deletes the note the action created, unless it has been
// edited, renamed or moved since: deleting it then would lose that work.
func (u undoer) uncreateNote(ctx context.Context) (string, after, error) {
	row, left, err := u.currentDocument(ctx)
	if err != nil {
		return "", after{}, err
	}
	if row.TextMd5 != left.TextMD5 || row.Title != left.Title || !sameID(row.FolderID, left.FolderID) {
		return "", after{}, refuse("The note %q has been changed since it was created, so it is not deleted: undoing would lose those changes.", row.Title)
	}
	chunks, err := u.q.ChunkIDsOfDocument(ctx, ChunkIDsOfDocumentParams{DocumentID: row.ID, UserID: u.who.userID})
	if err != nil {
		return "", after{}, err
	}
	if err := u.q.DeleteDocument(ctx, DeleteDocumentParams{ID: row.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Removed the note %q that was created.", row.Title), after{forget: chunks}, nil
}

// unedit puts a note's text back, unless it has been edited since.
func (u undoer) unedit(ctx context.Context) (string, after, error) {
	row, left, err := u.currentDocument(ctx)
	if err != nil {
		return "", after{}, err
	}
	if row.TextMd5 != left.TextMD5 {
		return "", after{}, refuse("The note %q has been edited again since, so its text is not put back: undoing would lose the newer edit.", row.Title)
	}
	revision, err := u.documentRevision(ctx)
	if err != nil {
		return "", after{}, err
	}
	if revision.ContentBefore == nil {
		return "", after{}, refuse("There is no record of the note's earlier text, so it cannot be undone.")
	}
	text := *revision.ContentBefore
	if err := u.q.SetNoteText(ctx, SetNoteTextParams{Markdown: text, ContentHash: notes.ContentHash(text), ID: row.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Put back the earlier text of %q.", row.Title), after{index: []int64{row.ID}}, nil
}

// unrenameDocument puts a title back, unless it has been renamed since. A
// change to its text or folder since is not the rename's, and is kept.
func (u undoer) unrenameDocument(ctx context.Context) (string, after, error) {
	row, left, err := u.currentDocument(ctx)
	if err != nil {
		return "", after{}, err
	}
	if row.Title != left.Title {
		return "", after{}, refuse("The document has been renamed again since (it is now %q), so its old name is not put back.", row.Title)
	}
	revision, err := u.documentRevision(ctx)
	if err != nil {
		return "", after{}, err
	}
	if revision.TitleBefore == nil {
		return "", after{}, refuse("There is no record of the document's earlier name, so it cannot be undone.")
	}
	if err := u.q.SetDocumentTitle(ctx, SetDocumentTitleParams{Title: *revision.TitleBefore, ID: row.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Renamed %q back to %q.", row.Title, *revision.TitleBefore), after{}, nil
}

// unmoveDocument files a document back where it was, unless it has been
// moved since, or the folder it was in is gone.
func (u undoer) unmoveDocument(ctx context.Context) (string, after, error) {
	row, left, err := u.currentDocument(ctx)
	if err != nil {
		return "", after{}, err
	}
	if !sameID(row.FolderID, left.FolderID) {
		return "", after{}, refuse("%q has been moved again since, so it is not moved back.", row.Title)
	}
	revision, err := u.documentRevision(ctx)
	if err != nil {
		return "", after{}, err
	}
	if err := u.requireFolderStill(ctx, revision.FolderIDBefore); err != nil {
		return "", after{}, err
	}
	if err := u.q.SetDocumentFolder(ctx, SetDocumentFolderParams{FolderID: revision.FolderIDBefore, ID: row.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Moved %q back to where it was.", row.Title), after{}, nil
}

// requireFolderStill refuses to put something back into a folder that has
// been deleted since. Putting it at the top level instead would be a place
// nobody chose.
func (u undoer) requireFolderStill(ctx context.Context, folderID *int64) error {
	if folderID == nil {
		return nil
	}
	exists, err := u.q.FolderExists(ctx, FolderExistsParams{ID: *folderID, UserID: u.who.userID})
	if err != nil {
		return err
	}
	if !exists {
		return refuse("The folder it was in has been deleted since. Bring that folder back first, then undo this.")
	}
	return nil
}

// undeleteNote writes a deleted note again from its revision: its title,
// text, folder and metadata, its note record, and the notebooks it was a
// source of. It gets a new id (the old row is gone); the revision is
// pointed at it, and it is indexed again.
func (u undoer) undeleteNote(ctx context.Context) (string, after, error) {
	revision, err := u.documentRevision(ctx)
	if err != nil {
		return "", after{}, err
	}
	if revision.TitleBefore == nil || revision.ContentBefore == nil {
		return "", after{}, refuse("There is no record of the deleted note's text, so it cannot be brought back.")
	}
	var kept deletedNote
	if len(revision.MetadataBefore) > 0 {
		if err := json.Unmarshal(revision.MetadataBefore, &kept); err != nil {
			return "", after{}, fmt.Errorf("reading the deleted note's record: %w", err)
		}
	}
	if err := u.requireFolderStill(ctx, revision.FolderIDBefore); err != nil {
		return "", after{}, err
	}
	text := *revision.ContentBefore
	var metadata []byte
	if trimmed := string(kept.DocumentMetadata); trimmed != "" && trimmed != "null" {
		metadata = kept.DocumentMetadata
	}
	id, err := u.q.InsertNote(ctx, InsertNoteParams{
		UserID: u.who.userID, Title: *revision.TitleBefore, Markdown: text, ContentHash: notes.ContentHash(text),
		NeedsIndexing: text != "", DocumentMetadata: metadata, FolderID: revision.FolderIDBefore,
	})
	if err != nil {
		return "", after{}, err
	}
	origin := kept.Note.Origin
	if origin == "" {
		origin = notes.OriginChat
	}
	if err := u.q.InsertNoteRecord(ctx, InsertNoteRecordParams{
		DocumentID: id, UserID: u.who.userID, NotebookID: kept.Note.NotebookID, Origin: origin,
		AutomationID: kept.Note.AutomationID, ThreadID: kept.Note.ThreadID, MessageID: kept.Note.MessageID,
		PinnedAt: kept.Note.PinnedAt,
	}); err != nil {
		return "", after{}, err
	}
	for _, source := range kept.Sources {
		if err := u.q.RestoreDocumentSource(ctx, RestoreDocumentSourceParams{
			DocumentID: id, Enabled: source.Enabled, NotebookID: source.NotebookID, UserID: u.who.userID,
		}); err != nil {
			return "", after{}, err
		}
	}
	if err := u.q.PointDocumentRevision(ctx, PointDocumentRevisionParams{DocumentID: id, ID: revision.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	owed := after{}
	if text != "" {
		owed.index = []int64{id}
	}
	return fmt.Sprintf("Brought back the note %q.", *revision.TitleBefore), owed, nil
}

// uncreateFolder deletes the folder the action made, unless it has been
// renamed or moved since, or anything has been put in it.
func (u undoer) uncreateFolder(ctx context.Context) (string, after, error) {
	row, left, err := u.currentFolder(ctx)
	if err != nil {
		return "", after{}, err
	}
	if row.Name != left.Name || !sameID(row.ParentID, left.ParentID) {
		return "", after{}, refuse("The folder %q has been renamed or moved since it was made, so it is not deleted.", row.Name)
	}
	empty, err := u.q.FolderIsEmpty(ctx, FolderIsEmptyParams{ID: row.ID, UserID: u.who.userID})
	if err != nil {
		return "", after{}, err
	}
	if !empty {
		return "", after{}, refuse("The folder %q has had things put in it since it was made, so it is not deleted.", row.Name)
	}
	if err := u.q.DeleteFolder(ctx, DeleteFolderParams{ID: row.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Removed the folder %q that was made.", row.Name), after{}, nil
}

// unrenameFolder puts a folder's name back, unless it has been renamed
// since or another folder beside it has taken the old name.
func (u undoer) unrenameFolder(ctx context.Context) (string, after, error) {
	row, left, err := u.currentFolder(ctx)
	if err != nil {
		return "", after{}, err
	}
	if row.Name != left.Name {
		return "", after{}, refuse("The folder has been renamed again since (it is now %q), so its old name is not put back.", row.Name)
	}
	revision, err := u.folderRevision(ctx)
	if err != nil {
		return "", after{}, err
	}
	if revision.NameBefore == nil {
		return "", after{}, refuse("There is no record of the folder's earlier name, so it cannot be undone.")
	}
	name := *revision.NameBefore
	taken, err := u.q.SiblingNamed(ctx, SiblingNamedParams{UserID: u.who.userID, Name: name, ExceptID: row.ID, ParentID: row.ParentID})
	if err != nil {
		return "", after{}, err
	}
	if taken {
		return "", after{}, refuse("Another folder beside it is called %q now, so the old name cannot be put back.", name)
	}
	if err := u.q.SetFolderName(ctx, SetFolderNameParams{Name: name, ID: row.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Renamed the folder %q back to %q.", row.Name, name), after{}, nil
}

// unmoveFolder puts a folder back under its old parent, at its old place
// among its siblings, unless it has been moved since, the old parent is
// gone or is now inside it, or the old place has a folder of its name.
func (u undoer) unmoveFolder(ctx context.Context) (string, after, error) {
	row, left, err := u.currentFolder(ctx)
	if err != nil {
		return "", after{}, err
	}
	if !sameID(row.ParentID, left.ParentID) {
		return "", after{}, refuse("The folder %q has been moved again since, so it is not moved back.", row.Name)
	}
	revision, err := u.folderRevision(ctx)
	if err != nil {
		return "", after{}, err
	}
	if revision.PositionBefore == nil {
		return "", after{}, refuse("There is no record of where the folder was, so it cannot be undone.")
	}
	parent := revision.ParentIDBefore
	if err := u.requireFolderStill(ctx, parent); err != nil {
		return "", after{}, err
	}
	if parent != nil {
		inside, err := u.q.IsWithin(ctx, IsWithinParams{Ancestor: row.ID, ID: *parent, UserID: u.who.userID})
		if err != nil {
			return "", after{}, err
		}
		if inside {
			return "", after{}, refuse("The folder it was in is now inside it, so it cannot be moved back.")
		}
	}
	below, err := u.q.SubtreeDepth(ctx, SubtreeDepthParams{ID: row.ID, UserID: u.who.userID})
	if err != nil {
		return "", after{}, err
	}
	// placeFolder checks the depth and the name; the position is the one
	// it had, so it sits among its old siblings where it sat.
	if _, err := placeFolder(ctx, u.q, u.who, parent, row.Name, row.ID, below); err != nil {
		return "", after{}, err
	}
	if err := u.q.SetFolderParent(ctx, SetFolderParentParams{ParentID: parent, Position: *revision.PositionBefore, ID: row.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Moved the folder %q back to where it was.", row.Name), after{}, nil
}

// undeleteFolder makes a deleted (empty) folder again, under its old parent
// at its old place, with the notebooks it was a source of.
func (u undoer) undeleteFolder(ctx context.Context) (string, after, error) {
	revision, err := u.folderRevision(ctx)
	if err != nil {
		return "", after{}, err
	}
	if revision.NameBefore == nil || revision.PositionBefore == nil {
		return "", after{}, refuse("There is no record of the deleted folder, so it cannot be brought back.")
	}
	name, parent := *revision.NameBefore, revision.ParentIDBefore
	if err := u.requireFolderStill(ctx, parent); err != nil {
		return "", after{}, err
	}
	if _, err := placeFolder(ctx, u.q, u.who, parent, name, 0, 0); err != nil {
		return "", after{}, err
	}
	id, err := u.q.InsertFolder(ctx, InsertFolderParams{UserID: u.who.userID, Name: name, Position: *revision.PositionBefore, ParentID: parent})
	if err != nil {
		return "", after{}, err
	}
	for _, source := range u.state.FolderSources {
		if err := u.q.RestoreFolderSource(ctx, RestoreFolderSourceParams{
			FolderID: id, Enabled: source.Enabled, NotebookID: source.NotebookID, UserID: u.who.userID,
		}); err != nil {
			return "", after{}, err
		}
	}
	if err := u.q.PointFolderRevision(ctx, PointFolderRevisionParams{FolderID: id, ID: revision.ID, UserID: u.who.userID}); err != nil {
		return "", after{}, err
	}
	return fmt.Sprintf("Brought back the folder %q.", name), after{}, nil
}
