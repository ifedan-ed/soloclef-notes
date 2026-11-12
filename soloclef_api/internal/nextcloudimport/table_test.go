package nextcloudimport

import (
	"context"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Two accounts: the caller of every test that does not say otherwise, and
// the other account of the isolation tests.
const (
	owner    = "11111111-2222-4333-8444-555555555555"
	somebody = "99999999-2222-4333-8444-555555555555"
)

// held is one document of the stand-in database.
type held struct {
	// owner is the account the document belongs to. Every method below
	// that takes an account finds a document only for its owner, as the
	// statements do.
	owner    string
	imported Imported
	parser   string
	files    []Downloaded
	reason   string
}

// table is the database for one test. The rules that live in SQL (the
// decision about a file seen before, the stale interval) are tested against
// Postgres in database_test.go; here Start simply makes a document per
// planned file, so the routes and the downloads are tested for what they
// do around the transaction.
type table struct {
	mu          sync.Mutex
	connections map[int64]struct {
		owner  string
		config string
	}
	// notebooks is each notebook's owner.
	notebooks map[int64]string
	documents map[int64]*held
	nextID    int64
	// starts is every import that reached the transaction.
	starts []struct {
		plan    Plan
		options Options
	}
	startErr error
	// events is the order things happened in, shared with the queue and
	// the downloads where a test cares.
	events *journal
}

// journal is the order things happened in, written from several
// goroutines.
type journal struct {
	mu      sync.Mutex
	entries []string
}

// note records an event; a nil journal records nothing.
func (j *journal) note(event string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, event)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.entries...)
}

func (t *table) note(event string) { t.events.note(event) }

func (t *table) Connection(_ context.Context, connectorID int64, caller string) ([]byte, bool, error) {
	connection, found := t.connections[connectorID]
	if !found || connection.owner != caller {
		return nil, false, nil
	}
	return []byte(connection.config), true, nil
}

func (t *table) OwnsNotebook(_ context.Context, notebookID int64, userID string) (bool, error) {
	return t.notebooks[notebookID] == userID, nil
}

// owned is the document for its owner, and nil for anybody else.
func (t *table) owned(documentID int64, userID string) *held {
	document, found := t.documents[documentID]
	if !found || document.owner != userID {
		return nil
	}
	return document
}

func (t *table) Start(_ context.Context, plan Plan, account Account, options Options) (Started, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startErr != nil {
		return Started{}, t.startErr
	}
	t.starts = append(t.starts, struct {
		plan    Plan
		options Options
	}{plan, options})
	var started Started
	for range plan.PickedFolders {
		t.nextID++
		started.FolderIDs = append(started.FolderIDs, t.nextID)
	}
	for _, file := range plan.Files {
		t.nextID++
		t.documents[t.nextID] = &held{owner: options.UserID, parser: options.Parser, imported: Imported{
			Title: file.Entry.Name, State: "pending", Stage: "downloading",
			ConnectorID: options.ConnectorID, ServerURL: account.BaseURL, Username: account.Username,
			Path: file.Entry.Path, ETag: file.Entry.ETag,
		}}
		started.DocumentIDs = append(started.DocumentIDs, t.nextID)
		started.Jobs = append(started.Jobs, Job{DocumentID: t.nextID, UserID: options.UserID, Account: account,
			Path: file.Entry.Path, Name: file.Entry.Name, ContentType: file.Entry.ContentType})
	}
	t.note("committed")
	return started, nil
}

func (t *table) Imported(_ context.Context, documentID int64, userID string) (*Imported, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	document := t.owned(documentID, userID)
	if document == nil {
		return nil, nil
	}
	copied := document.imported
	return &copied, nil
}

func (t *table) QueueRefresh(_ context.Context, documentID int64, userID string, connectorID int64, entry Entry) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	document := t.owned(documentID, userID)
	if document == nil {
		return false, nil
	}
	document.imported.State, document.imported.Stage = "pending", "downloading"
	document.imported.ETag, document.imported.ConnectorID = entry.ETag, connectorID
	return true, nil
}

func (t *table) RecordDownloaded(_ context.Context, file Downloaded) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	document := t.owned(file.DocumentID, file.UserID)
	if document == nil || document.imported.State != "pending" || document.imported.Stage != "downloading" {
		return false, nil
	}
	document.imported.Stage = ""
	document.files = append(document.files, file)
	t.note("file row")
	return true, nil
}

func (t *table) MarkFailed(_ context.Context, documentID int64, userID, reason string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if document := t.owned(documentID, userID); document != nil && document.imported.State == "pending" && document.imported.Stage == "downloading" {
		document.imported.State, document.imported.Stage, document.reason = "failed", "", reason
	}
	return nil
}

func (t *table) document(id int64) held {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.documents[id]
}

// queue is the indexing queue for one test.
type queue struct {
	mu     sync.Mutex
	queued []int64
	events *journal
}

func (q *queue) Enqueue(_ context.Context, documentID int64, _ produce.LogContext) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queued = append(q.queued, documentID)
	q.events.note("queued")
	return nil
}

func (q *queue) ids() []int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]int64(nil), q.queued...)
}

// handed is a Starter that only records what it was given.
type handed struct {
	jobs   []Job
	events *journal
}

func (h *handed) Start(jobs []Job, _ produce.LogContext) {
	h.jobs = append(h.jobs, jobs...)
	h.events.note("downloads handed over")
}
