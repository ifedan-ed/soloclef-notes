package threads

import (
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// A notebook's own chats: the list, starting one, and clearing them. They
// are this package's because clearing a chat is deleting a thread, and a
// thread is deleted one way only: its turn stopped and its saved
// conversation forgotten before its rows go (remove, in routes.go).
const (
	NotebookThreadsPath = "/api/v1/notebooks/{notebook_id}/threads"
	notebookAbsent      = "Notebook not found"
)

// notebookThread is one chat as the notebook pane lists it.
type notebookThread struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// notebookFor reads the path's notebook id and answers the 404 itself for
// a notebook that is not the caller's.
func (r Routes) notebookFor(answer answerer) (userID string, notebookID int64, ok bool) {
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return "", 0, false
	}
	notebookID, ok = answer.pathID("notebook_id")
	if !ok {
		return "", 0, false
	}
	owned := false
	if notebookID > 0 {
		var err error
		owned, err = New(r.Service.Store.Pool).OwnsNotebook(answer.ctx.Context(),
			OwnsNotebookParams{NotebookID: notebookID, UserID: caller.UserID})
		if err != nil {
			answer.fail("could not look for the notebook", err)
			return "", 0, false
		}
	}
	if !owned {
		answer.errors.Write(answer.response, http.StatusNotFound, notebookAbsent, answer.requestID)
		return "", 0, false
	}
	return caller.UserID, notebookID, true
}

func (r Routes) notebookThreads(ctx huma.Context) {
	answer := r.answerer(ctx, "a notebook's chats could not be listed")
	userID, notebookID, ok := r.notebookFor(answer)
	if !ok {
		return
	}
	rows, err := New(r.Service.Store.Pool).NotebookThreads(ctx.Context(),
		NotebookThreadsParams{NotebookID: &notebookID, UserID: userID})
	if err != nil {
		answer.fail("could not read the notebook's chats", err)
		return
	}
	listed := make([]notebookThread, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, notebookThread{ID: row.ID, Title: row.Title, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}
	answer.response.Header().Set("Cache-Control", "private, no-store")
	answer.send(http.StatusOK, listed)
}

// startNotebookThread starts a chat in the notebook, which reads the
// notebook's sources, and marks the notebook used. It takes no body.
func (r Routes) startNotebookThread(ctx huma.Context) {
	answer := r.answerer(ctx, "a notebook's chat could not be started")
	userID, notebookID, ok := r.notebookFor(answer)
	if !ok {
		return
	}
	now := r.Service.Now().UTC()
	q := New(r.Service.Store.Pool)
	if _, err := q.TouchOwnedNotebook(ctx.Context(), TouchOwnedNotebookParams{UsedAt: now, NotebookID: notebookID, UserID: userID}); err != nil {
		answer.fail("could not mark the notebook used", err)
		return
	}
	row, err := q.InsertNotebookThread(ctx.Context(), InsertNotebookThreadParams{
		Title: defaultThreadTitle, UserID: userID, NotebookID: &notebookID, MadeAt: now,
	})
	if err != nil {
		answer.fail("could not write the chat", err)
		return
	}
	answer.send(http.StatusCreated, notebookThread{ID: row.ID, Title: row.Title, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
}

// clearNotebookThreads deletes every chat of the notebook but the studio's
// own threads, each the way one thread is deleted. A chat whose answer is
// still stopping is a 409 to retry, as a single delete is; the chats
// deleted before it stay deleted, which a retry then finds gone.
func (r Routes) clearNotebookThreads(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "a notebook's chats could not be cleared")
	userID, notebookID, ok := r.notebookFor(answer)
	if !ok {
		return
	}
	ids, err := New(s.Store.Pool).NotebookThreadIDs(ctx.Context(), NotebookThreadIDsParams{NotebookID: &notebookID, UserID: userID})
	if err != nil {
		answer.fail("could not read the notebook's chats", err)
		return
	}
	for _, id := range ids {
		release, err := s.hold(ctx.Context(), id)
		if errors.Is(err, errStillStopping) {
			answer.response.Header().Set("Retry-After", "1")
			answer.refuse(&refusal{status: http.StatusConflict, code: codeTurnCancelling, message: stillStopping}, "")
			return
		}
		if err != nil {
			answer.fail("could not claim a chat", err)
			return
		}
		err = s.Forget(ctx.Context(), id)
		if err == nil {
			err = s.Store.Delete(ctx.Context(), userID, id)
		}
		release()
		if err != nil {
			answer.fail("could not delete a chat", err)
			return
		}
	}
	answer.response.WriteHeader(http.StatusNoContent)
}
