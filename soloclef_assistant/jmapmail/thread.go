package jmapmail

import (
	"context"
	"slices"
)

// MostThreadMessages is the most messages of one thread that are read: the
// newest ones, which are what a reply is written to.
const MostThreadMessages = 20

// Thread is the conversation a message belongs to, oldest first, its
// newest MostThreadMessages messages; more says the thread is longer.
//
// It is two requests. The first finds the thread's ids from the message
// (Email/get for its threadId, then Thread/get by back-reference); the
// second fetches the newest of them whole. One request could fetch them
// all by back-reference, but a back-reference cannot be cut, and a thread
// of a thousand messages would then be read whole.
func (c Client) Thread(ctx context.Context, session Session, credential Credential, emailID string) ([]Email, bool, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	got, err := c.call(ctx, session, credential, mailUsing,
		invocation{"Email/get", map[string]any{
			"accountId": session.AccountID, "ids": []string{emailID}, "properties": []string{"threadId"},
		}, "e"},
		invocation{"Thread/get", map[string]any{
			"accountId": session.AccountID,
			"#ids":      backReference{ResultOf: "e", Name: "Email/get", Path: "/list/*/threadId"},
		}, "t"})
	if err != nil {
		return nil, false, err
	}
	var located struct {
		List []struct {
			ID string `json:"id"`
		} `json:"list"`
	}
	if err := got.decode("e", "Email/get", &located); err != nil {
		return nil, false, err
	}
	if len(located.List) != 1 {
		return nil, false, errNotFound
	}
	var threads struct {
		List []struct {
			EmailIDs []string `json:"emailIds"`
		} `json:"list"`
	}
	if err := got.decode("t", "Thread/get", &threads); err != nil {
		return nil, false, err
	}
	if len(threads.List) != 1 {
		return nil, false, errNotFound
	}
	// A thread's ids are oldest first (RFC 8621 3).
	ids := threads.List[0].EmailIDs
	most := min(MostThreadMessages, session.MaxObjectsInGet)
	more := len(ids) > most
	if more {
		ids = ids[len(ids)-most:]
	}
	got, err = c.call(ctx, session, credential, mailUsing,
		invocation{"Email/get", emailGet(session.AccountID, ids, threadBodyBytes), "m"})
	if err != nil {
		return nil, false, err
	}
	var fetched struct {
		List []fullEmail `json:"list"`
	}
	if err := got.decode("m", "Email/get", &fetched); err != nil {
		return nil, false, err
	}
	out := make([]Email, 0, len(fetched.List))
	for _, email := range fetched.List {
		out = append(out, email.email())
	}
	// In the thread's order, whatever order /get answered in.
	position := make(map[string]int, len(ids))
	for index, id := range ids {
		position[id] = index
	}
	slices.SortStableFunc(out, func(a, b Email) int { return position[a.ID] - position[b.ID] })
	return out, more, nil
}
