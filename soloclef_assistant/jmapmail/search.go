package jmapmail

import (
	"context"
	"time"
)

// MostResults is the most messages one search returns.
const MostResults = 50

// Query is one search. Every field set must match.
type Query struct {
	// MailboxID is the folder searched; "" searches every folder.
	MailboxID string
	// Text matches anywhere in a message; From, To and Subject in that
	// header.
	Text, From, To, Subject string
	// After and Before bound when the message was received; zero is
	// unbounded. Before is exclusive.
	After, Before time.Time
	// UnreadOnly leaves out messages already read.
	UnreadOnly bool
	// Limit is how many to return, newest first: 1 to MostResults.
	Limit int
}

// Summary is one message as a search lists it.
type Summary struct {
	ID, ThreadID string
	From, To     string
	Subject      string
	ReceivedAt   time.Time
	Unread       bool
	// Preview is the server's short plain-text start of the message.
	Preview string
}

// summaryProperties is what a search fetches of each message: enough to
// list it, nothing of its body but the server's preview.
var summaryProperties = []string{"id", "threadId", "from", "to", "subject", "receivedAt", "keywords", "preview"}

// listedEmail is the part of an Email object a summary or a read uses.
type listedEmail struct {
	ID         string          `json:"id"`
	ThreadID   string          `json:"threadId"`
	From       []Address       `json:"from"`
	To         []Address       `json:"to"`
	Subject    string          `json:"subject"`
	ReceivedAt time.Time       `json:"receivedAt"`
	Keywords   map[string]bool `json:"keywords"`
	Preview    string          `json:"preview"`
}

func (e listedEmail) summary() Summary {
	return Summary{ID: e.ID, ThreadID: e.ThreadID, From: joined(e.From), To: joined(e.To), Subject: e.Subject,
		ReceivedAt: e.ReceivedAt, Unread: !e.Keywords["$seen"], Preview: e.Preview}
}

// jmapTime is RFC 8620's UTCDate.
func jmapTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// filter is the query as an RFC 8621 FilterCondition: only the fields set.
func (q Query) filter() map[string]any {
	condition := map[string]any{}
	set := func(key, value string) {
		if value != "" {
			condition[key] = value
		}
	}
	set("inMailbox", q.MailboxID)
	set("text", q.Text)
	set("from", q.From)
	set("to", q.To)
	set("subject", q.Subject)
	if !q.After.IsZero() {
		condition["after"] = jmapTime(q.After)
	}
	if !q.Before.IsZero() {
		condition["before"] = jmapTime(q.Before)
	}
	if q.UnreadOnly {
		condition["notKeyword"] = "$seen"
	}
	return condition
}

// Search lists the newest messages that match, and whether more matched
// than are listed. The query and the fetch of the summaries are one
// request: the fetch takes its ids from the query by back-reference.
func (c Client) Search(ctx context.Context, session Session, credential Credential, query Query) ([]Summary, bool, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	limit := min(max(query.Limit, 1), MostResults)
	got, err := c.call(ctx, session, credential, mailUsing,
		invocation{"Email/query", map[string]any{
			"accountId":      session.AccountID,
			"filter":         query.filter(),
			"sort":           []map[string]any{{"property": "receivedAt", "isAscending": false}},
			"limit":          limit,
			"calculateTotal": true,
		}, "q"},
		invocation{"Email/get", map[string]any{
			"accountId":  session.AccountID,
			"#ids":       backReference{ResultOf: "q", Name: "Email/query", Path: "/ids"},
			"properties": summaryProperties,
		}, "g"})
	if err != nil {
		return nil, false, err
	}
	var queried struct {
		IDs   []string `json:"ids"`
		Total *int     `json:"total"`
	}
	if err := got.decode("q", "Email/query", &queried); err != nil {
		return nil, false, err
	}
	var fetched struct {
		List []listedEmail `json:"list"`
	}
	if err := got.decode("g", "Email/get", &fetched); err != nil {
		return nil, false, err
	}
	// The query's order is the answer's: /get returns objects in no
	// promised order.
	byID := make(map[string]listedEmail, len(fetched.List))
	for _, email := range fetched.List {
		byID[email.ID] = email
	}
	out := make([]Summary, 0, len(queried.IDs))
	for _, id := range queried.IDs {
		if email, found := byID[id]; found {
			out = append(out, email.summary())
		}
	}
	// A server that will not count says nothing about more; one that does
	// is believed. A full page from a server that does not count may hide
	// more, and is said to.
	more := len(queried.IDs) >= limit
	if queried.Total != nil {
		more = *queried.Total > len(queried.IDs)
	}
	return out, more, nil
}
