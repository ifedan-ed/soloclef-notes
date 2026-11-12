package threads

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// publicURL is where a link opens.
func (s *Service) publicURL(token string) string {
	return strings.TrimRight(s.FrontendURL, "/") + "/public/" + token
}

// share makes a public link to the thread as it stands now.
//
// The thread is the caller's own: nobody can publish somebody else's chat.
func (r Routes) share(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "a public link could not be made")
	_, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	days, expires, failures := readExpiry(requestbody.Buffered(ctx.Context()))
	if len(failures) > 0 {
		answer.invalid(failures)
		return
	}
	now := s.Now()
	link := NewLink{ThreadID: thread.ID, UserID: thread.UserID}
	if expires {
		at := now.Add(time.Duration(days) * 24 * time.Hour)
		link.ExpiresAt = &at
	}
	var err error
	if link.Data, link.Hash, link.MessageIDs, err = s.copyOf(ctx.Context(), thread, now); err != nil {
		answer.refuse(err, "could not build the public copy")
		return
	}
	if link.Token, err = newToken(); err != nil {
		answer.fail("could not make a link's token", err)
		return
	}
	saved, err := s.Store.SaveLink(ctx.Context(), link)
	if err != nil {
		answer.fail("could not write the link", err)
		return
	}
	out := createdLink{SnapshotID: saved.ID, ShareToken: saved.Token, PublicURL: s.publicURL(saved.Token), IsNew: saved.IsNew}
	if saved.ExpiresAt != nil {
		at := pythonjson.PydanticDateTime(*saved.ExpiresAt)
		out.ExpiresAt = &at
	}
	answer.send(http.StatusOK, out)
}

// copyOf builds the thread's public copy: the stored document, the hash
// that says whether it changed, and the ids of the messages in it.
func (s *Service) copyOf(ctx context.Context, thread Thread, now time.Time) (data []byte, hash string, messageIDs []int64, err error) {
	rows, err := s.Store.Messages(ctx, thread.UserID, thread.ID, 0, 0)
	if err != nil {
		return nil, "", nil, err
	}
	if len(rows) == 0 {
		return nil, "", nil, &refusal{status: http.StatusBadRequest, message: emptyChat}
	}
	messages := make([]publicMessage, len(rows))
	messageIDs = make([]int64, len(rows))
	for i, row := range rows {
		parts, err := publicParts(row.Content)
		if err != nil {
			return nil, "", nil, err
		}
		message := publicMessage{id: row.ID, role: strings.ToLower(row.Role), parts: parts, createdAt: row.CreatedAt}
		// A stranger learns a name and nothing else of an author. An
		// author with no name set is "User"; one whose account is gone has
		// no author_id left on the row, and so no author here.
		if row.AuthorID != nil {
			name := "User"
			if row.AuthorName != nil && *row.AuthorName != "" {
				name = *row.AuthorName
			}
			message.author = publicAuthor(name)
		}
		messages[i] = message
		messageIDs[i] = row.ID
	}

	podcastIDs, artifactIDs := referenced(messages)
	ready, err := s.Store.ReadyPodcasts(ctx, thread.UserID, podcastIDs)
	if err != nil {
		return nil, "", nil, err
	}
	podcasts, more, err := embedPodcasts(messages, ready)
	if err != nil {
		return nil, "", nil, err
	}
	// What the link may serve to anyone: the artifacts its cards name that
	// are really this account's.
	served, err := s.Store.OwnedArtifacts(ctx, thread.UserID, append(artifactIDs, more...))
	if err != nil {
		return nil, "", nil, err
	}

	// The hash is taken after the podcast cards were brought up to date:
	// a podcast that has finished since the last share is a different
	// page, and gets a different address.
	if hash, err = contentHash(messages); err != nil {
		return nil, "", nil, err
	}
	// The page says whose conversation it is by a display name and
	// nothing else. The owner exists: the thread was just read as theirs.
	name, found, err := s.Store.AccountName(ctx, thread.UserID)
	if err != nil {
		return nil, "", nil, err
	}
	var author any
	switch {
	case !found:
		author = publicAuthor("Unknown User")
	case name == nil || *name == "":
		author = publicAuthor("User")
	default:
		author = publicAuthor(*name)
	}
	data, err = linkData(thread.Title, now, author, messages, podcasts, served)
	return data, hash, messageIDs, err
}

// links lists the thread's live links. A link is the conversation, so
// only the thread's owner is told them.
func (r Routes) links(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "a thread's public links could not be listed")
	caller, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	found, err := s.Store.Links(ctx.Context(), caller.UserID, thread.ID)
	if err != nil {
		answer.fail("could not read the links", err)
		return
	}
	out := linkList{Snapshots: make([]listedLink, len(found))}
	for i, link := range found {
		out.Snapshots[i] = listedLink{
			ID: link.ID, ShareToken: link.Token, PublicURL: s.publicURL(link.Token),
			CreatedAt: pythonjson.ISOFormat(link.CreatedAt), MessageCount: link.Messages,
		}
	}
	answer.send(http.StatusOK, out)
}

// unshare takes one link down.
//
// One statement decides and acts: the link, by its id, of this thread, of
// the caller's account. A link that is not there, one of another thread and
// one of another account's thread are the same "Snapshot not found", so the
// pair of numbers tells a stranger nothing.
func (r Routes) unshare(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "a public link could not be taken down")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	threadID, ok := answer.pathID("thread_id")
	if !ok {
		return
	}
	linkID, ok := answer.pathID("snapshot_id")
	if !ok {
		return
	}
	deleted, err := s.Store.DeleteLink(ctx.Context(), caller.UserID, threadID, linkID)
	if err != nil {
		answer.fail("could not delete the link", err)
		return
	}
	if !deleted {
		answer.refuse(&refusal{status: http.StatusNotFound, message: linkAbsent}, "")
		return
	}
	answer.send(http.StatusOK, said{Message: linkDeleted})
}
