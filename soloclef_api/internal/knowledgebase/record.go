package knowledgebase

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// Indexing queues a document's index_document job once its rows have
// committed (indexqueue.Indexing). A job that could not be queued has
// already marked the document failed, with a reason the person can act on,
// when the error comes back.
type Indexing interface {
	Enqueue(ctx context.Context, documentID int64, logContext produce.LogContext) error
}

// Vectors removes passages' points from the vector store
// (documentdeletion.Qdrant). A point's id is its chunk's id.
type Vectors interface {
	Delete(ctx context.Context, chunkIDs []int64) error
}

// Changes is what a change and its undo are made with. The specialist's
// tools and the undo routes share one, so a change and its undo index and
// remove in the same way.
type Changes struct {
	Pool     *pgxpool.Pool
	Indexing Indexing
	Vectors  Vectors
	Logger   *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (c Changes) Check() error {
	switch {
	case c.Pool == nil:
		return errors.New("knowledgebase: the database pool is not set")
	case c.Indexing == nil:
		return errors.New("knowledgebase: the indexing queue is not set")
	case c.Vectors == nil:
		return errors.New("knowledgebase: the vector store is not set")
	case c.Logger == nil:
		return errors.New("knowledgebase: the logger is not set")
	}
	return nil
}

// owner is whose knowledge base a change is made in and where it is logged.
type owner struct {
	userID   string
	threadID int64
	// notebookID is the notebook of a notebook's chat, or nil in a plain
	// chat. readable is then the documents that chat may read the text of
	// (the notebook's sources); nil in a plain chat, which may read all.
	notebookID *int64
	readable   map[int64]bool
}

// confined reports whether the chat is a notebook's, whose reading of text
// is held to the notebook.
func (o owner) confined() bool { return o.notebookID != nil }

// The kinds of item a change is about, in a row's result_id.
const (
	documentItem = "document"
	folderItem   = "folder"
)

// descriptor is an action row's reverse_descriptor: the item as the change
// left it, which an undo compares the item now with, so that an undo never
// writes over a change made since. After a delete there is no item; Document
// and Folder are both nil, and what the delete took with it beyond the
// revision's columns is kept here.
type descriptor struct {
	Document *documentState `json:"document,omitempty"`
	Folder   *folderState   `json:"folder,omitempty"`
	// FolderSources are the notebooks a deleted folder was a source of.
	// folder_revisions has no column for them.
	FolderSources []notebookSource `json:"folder_sources,omitempty"`
}

// documentState is a document as far as a change here can alter it.
// TextMD5 is md5 of its Markdown, the digest Postgres computes in the same
// statement that locks the row (LockDocument), so the comparison is of two
// values of one function.
type documentState struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	FolderID *int64 `json:"folder_id"`
	TextMD5  string `json:"text_md5"`
}

// folderState is a folder as far as a change here can alter it.
type folderState struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
}

// notebookSource is one notebook a document or folder was switched on in.
type notebookSource struct {
	NotebookID int64 `json:"notebook_id"`
	Enabled    bool  `json:"enabled"`
}

// noteRecord is a deleted note's notes row, kept in its revision's
// metadata_before so an undo files it where it was.
type noteRecord struct {
	NotebookID   *int64     `json:"notebook_id"`
	Origin       string     `json:"origin"`
	AutomationID *int64     `json:"automation_id"`
	ThreadID     *int64     `json:"thread_id"`
	MessageID    *int64     `json:"message_id"`
	PinnedAt     *time.Time `json:"pinned_at"`
}

// deletedNote is a deleted note's metadata_before: everything about it the
// revision's own columns do not hold.
type deletedNote struct {
	DocumentMetadata json.RawMessage  `json:"document_metadata,omitempty"`
	Note             noteRecord       `json:"note"`
	Sources          []notebookSource `json:"notebook_sources,omitempty"`
}

// documentBefore is a document_revisions row: the document as it was.
type documentBefore struct {
	documentID *int64
	content    *string
	title      *string
	folderID   *int64
	metadata   []byte
}

// folderBefore is a folder_revisions row.
type folderBefore struct {
	folderID *int64
	name     *string
	parentID *int64
	position *string
}

// change is one change's record.
type change struct {
	// tool is the tool that made it, which is also how an undo knows what
	// to do: dispatch is on the exact name (undo.go).
	tool string
	call tool.Call
	// item and itemID name what was changed, for the row's result_id.
	item   string
	itemID int64
	after  descriptor
	// One of the two, for the revision of the item as it was.
	document *documentBefore
	folder   *folderBefore
}

// logged is a recorded change, for the frame that lights the card's undo.
type logged struct {
	id         int64
	created    time.Time
	toolCallID string
	turnID     string
	tool       string
}

// record writes the change's action row and its revision, inside the
// change's transaction. The turn is the run's (assembly.WithTurnID): a write
// whose turn is unknown is refused rather than logged under no turn, which
// would leave "undo this turn" silently short of it.
func record(ctx context.Context, tx pgx.Tx, who owner, c change) (logged, error) {
	turnID, known := assembly.TurnIDOf(ctx)
	if !known {
		return logged{}, errors.New("knowledgebase: the chat turn of this change is not known, so it cannot be recorded")
	}
	after, err := json.Marshal(c.after)
	if err != nil {
		return logged{}, err
	}
	callID := cardID(c.call)
	resultID := c.item + ":" + strconv.FormatInt(c.itemID, 10)
	q := New(tx)
	row, err := q.InsertAction(ctx, InsertActionParams{
		ThreadID: who.threadID, UserID: who.userID, ToolCallID: &callID, ChatTurnID: &turnID,
		ToolName: c.tool, Args: argsObject(c.call.Args), ResultID: &resultID, Reversible: true,
		ReverseDescriptor: after,
	})
	if err != nil {
		return logged{}, fmt.Errorf("recording the change: %w", err)
	}
	switch {
	case c.document != nil:
		err = q.InsertDocumentRevision(ctx, InsertDocumentRevisionParams{
			DocumentID: c.document.documentID, UserID: who.userID, ContentBefore: c.document.content,
			TitleBefore: c.document.title, FolderIDBefore: c.document.folderID, MetadataBefore: c.document.metadata,
			CreatedByTurnID: &turnID, AgentActionID: &row.ID,
		})
	case c.folder != nil:
		err = q.InsertFolderRevision(ctx, InsertFolderRevisionParams{
			FolderID: c.folder.folderID, UserID: who.userID, NameBefore: c.folder.name,
			ParentIDBefore: c.folder.parentID, PositionBefore: c.folder.position,
			CreatedByTurnID: &turnID, AgentActionID: &row.ID,
		})
	default:
		err = errors.New("a change has no before-state to record")
	}
	if err != nil {
		return logged{}, fmt.Errorf("recording the item as it was: %w", err)
	}
	return logged{id: row.ID, created: row.CreatedAt, toolCallID: callID, turnID: turnID, tool: c.tool}, nil
}

// argsObject is the call's arguments as the log keeps them: the object the
// model sent (the loop has checked it is one), or SQL NULL for none.
func argsObject(args json.RawMessage) []byte {
	if len(strings.TrimSpace(string(args))) == 0 {
		return nil
	}
	return args
}

// cardID is the id the page knows the call's card by. A nested agent's
// thread is its parent's thread, "/", and the delegating call's id
// (agent.childThread), and the run stamps the same path onto every event of
// the nested run (the nested sink), so the card of call "w1" made inside
// delegate call "d1" of thread "chat-7" is "d1/w1": the thread with its first
// segment, the chat's own thread, taken off, then the call. A call of the
// main agent is its own id.
func cardID(call tool.Call) string {
	_, nested, found := strings.Cut(call.Thread, "/")
	if !found || nested == "" {
		return call.ID
	}
	return nested + "/" + call.ID
}

// actionLogFrame is the "data-action-log" frame that lights the card's undo
// button as soon as the change commits, without the page asking for the
// log. The names are the page's (soloclef_web/lib/chat/streaming-state.ts).
type actionLogFrame struct {
	ID                int64  `json:"id"`
	ToolCallID        string `json:"lc_tool_call_id"`
	ChatTurnID        string `json:"chat_turn_id"`
	ToolName          string `json:"tool_name"`
	Reversible        bool   `json:"reversible"`
	DescriptorPresent bool   `json:"reverse_descriptor_present"`
	Error             bool   `json:"error"`
	CreatedAt         string `json:"created_at"`
}

// announce tells the page about a recorded change. A frame that cannot be
// sent costs only the button's immediacy (the page reads the log when it
// next asks), so it is logged and the change stands.
func (c Changes) announce(ctx context.Context, call tool.Call, l logged) {
	if call.Notify == nil {
		return
	}
	err := call.Notify("action-log", actionLogFrame{
		ID: l.id, ToolCallID: l.toolCallID, ChatTurnID: l.turnID, ToolName: l.tool,
		Reversible: true, DescriptorPresent: true, CreatedAt: l.created.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		c.Logger.WarnContext(ctx, "a knowledge-base change could not be announced to the page", "action_id", l.id, "error", err.Error())
	}
}

// index queues a document's indexing after its change committed. The
// answer is a sentence for the model to pass on when the job could not be
// queued: the change stands, and the document says it was not indexed.
func (c Changes) index(ctx context.Context, who owner, documentID int64) string {
	logContext := produce.LogContext{UserID: who.userID}
	if who.threadID != 0 {
		logContext.ThreadID = strconv.FormatInt(who.threadID, 10)
	}
	if err := c.Indexing.Enqueue(ctx, documentID, logContext); err != nil {
		return " It is saved, but its indexing could not be started, so a search will not find it until it is indexed again."
	}
	return ""
}

// forgetPassages removes a deleted note's points from the vector store. The
// rows are already gone, and every search reads its hits back from them, so
// a point left behind is never returned; a failure is logged and nothing
// else.
func (c Changes) forgetPassages(ctx context.Context, chunkIDs []int64) {
	if len(chunkIDs) == 0 {
		return
	}
	if err := c.Vectors.Delete(ctx, chunkIDs); err != nil {
		c.Logger.WarnContext(ctx, "a deleted note's passages could not be removed from the vector store; no search can return them",
			"points", len(chunkIDs), "error", err.Error())
	}
}

// textMD5 is md5 of a text as Postgres's md5() computes it over the same
// UTF-8 bytes.
func textMD5(text string) string {
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:])
}

// sameID reports whether two nullable ids are the same id or both none.
func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
