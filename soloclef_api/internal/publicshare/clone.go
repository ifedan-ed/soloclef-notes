package publicshare

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The notebook a copied chat is filed in when its account has none for
// chats that began outside a notebook. The web app opens a chat only inside
// a notebook, so a copy with no notebook would have nowhere to open. The
// title and icon are the ones the first filing of such chats used.
const (
	earlierChatsTitle = "Earlier chats"
	earlierChatsIcon  = "\U0001f4ac"
)

// errNotAChat says the link shares a Studio output, which has no messages
// to copy.
var errNotAChat = errors.New("publicshare: the link is not a chat")

// copied is what the caller is told: where the copy is.
type copied struct {
	ThreadID   int64 `json:"thread_id"`
	NotebookID int64 `json:"notebook_id"`
}

// clone copies a shared chat into the caller's own account.
//
// What is copied is the link's frozen copy and nothing else: the text and
// the cards a stranger is shown. The original thread is never read, so a
// copy cannot carry anything its owner did not publish. The files behind
// the cards are not copied: a card in the copy still names the original
// account's artifact, which the copy's owner cannot open in their own
// account (the public page goes on showing it for as long as the link
// lives).
//
// Everything written carries the caller's id, in one transaction: the
// notebook if the account has none for such chats, the thread, its filing,
// and the messages. The Go chat reads a thread it has no transcript of from
// its saved messages, so the copy can be continued at once.
func (r Routes) clone(x *exchange) {
	caller, ok := x.signedIn()
	if !ok {
		return
	}
	// A person's own action in the app: an API key or an agent copying
	// links into the account is not what the button is for.
	if !caller.IsSession() {
		x.refuse(http.StatusForbidden, sessionRequired)
		return
	}
	token, now := x.ctx.Param("share_token"), r.now()
	made, err := database.Write(x.ctx.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (copied, error) {
		q := New(tx)
		link, err := q.LinkToCopy(ctx, LinkToCopyParams{ShareToken: token, Now: now})
		if err != nil {
			return copied{}, err
		}
		if link.ThreadID == nil {
			return copied{}, errNotAChat
		}
		// Taken after the link is known to be copyable, so that a wrong
		// token never queues behind a real copy.
		if err := q.LockAccountCopies(ctx, caller.UserID); err != nil {
			return copied{}, err
		}
		notebookID, err := q.EarlierChatsNotebook(ctx, caller.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			icon := earlierChatsIcon
			notebookID, err = q.CreateEarlierChatsNotebook(ctx, CreateEarlierChatsNotebookParams{
				UserID: caller.UserID, Title: earlierChatsTitle, Icon: &icon})
		}
		if err != nil {
			return copied{}, err
		}
		threadID, err := q.InsertCopiedThread(ctx, InsertCopiedThreadParams{
			UserID: caller.UserID, Title: link.Title, NotebookID: &notebookID,
			SourceThreadID: link.ThreadID, SnapshotID: &link.ID, MadeAt: now})
		if err != nil {
			return copied{}, err
		}
		if err := q.FileEarlierChat(ctx, FileEarlierChatParams{
			ThreadID: threadID, NotebookID: notebookID, UserID: caller.UserID}); err != nil {
			return copied{}, err
		}
		if _, err := q.CopyMessages(ctx, CopyMessagesParams{ThreadID: threadID, MadeAt: now, SnapshotID: link.ID}); err != nil {
			return copied{}, err
		}
		return copied{ThreadID: threadID, NotebookID: notebookID}, nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Never made, revoked and expired are one answer.
		x.refuse(http.StatusNotFound, linkAbsent)
		return
	case errors.Is(err, errNotAChat):
		x.refuse(http.StatusBadRequest, notAChat)
		return
	case err != nil:
		x.fail("could not copy a shared chat", err)
		return
	}
	r.Analytics.Record(caller, "public_chat_cloned", func(properties *pythonjson.Object) {
		properties.Set("chat_id", made.ThreadID)
	})
	x.send(http.StatusOK, made)
}
