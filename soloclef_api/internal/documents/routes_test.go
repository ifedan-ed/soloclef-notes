package documents

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/indexqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// The two accounts of every test here: one that owns the library below, and
// one that owns nothing.
const (
	owner    = "11111111-2222-4333-8444-555555555555"
	stranger = "99999999-2222-4333-8444-555555555555"
)

// library is the rows behind every route, held by account. Each method
// applies the predicate its statement carries (the id AND the owner), so a
// route that passed the wrong account, or none, reads nothing here exactly
// as it would from Postgres. store_database_test.go shows the statements
// themselves do the same.
type library struct {
	// documents, chunks and folders, each under the account that owns it.
	documents map[int64]libraryDocument
	chunks    map[int64]libraryChunk
	watched   map[string][]folders.Folder
	versions  map[int64][]Version

	restored []RestoreWork
	queued   []produce.Message
	// asked is every account a statement was issued for.
	asked []string
}

type libraryDocument struct {
	owner  string
	record Record
	status string
}

type libraryChunk struct {
	owner   string
	passage Passage
	place   int64
}

func (l *library) owns(documentID int64, userID string) bool {
	l.asked = append(l.asked, userID)
	held, found := l.documents[documentID]
	return found && held.owner == userID
}

func (l *library) Owns(_ context.Context, documentID int64, userID string) (bool, error) {
	return l.owns(documentID, userID), nil
}

func (l *library) CitedChunk(_ context.Context, chunkID int64, userID string) (*Cited, error) {
	l.asked = append(l.asked, userID)
	chunk, found := l.chunks[chunkID]
	if !found || chunk.owner != userID {
		return nil, nil
	}
	return &Cited{ID: chunkID, DocumentID: chunk.passage.DocumentID, Position: chunk.place}, nil
}

func (l *library) RecordOf(_ context.Context, documentID int64, userID string) (*Record, error) {
	if !l.owns(documentID, userID) {
		return nil, nil
	}
	record := l.documents[documentID].record
	return &record, nil
}

// passages is the account's chunks of a document, in reading order.
func (l *library) passages(documentID int64, userID string) []libraryChunk {
	l.asked = append(l.asked, userID)
	var found []libraryChunk
	for _, chunk := range l.chunks {
		if chunk.owner == userID && chunk.passage.DocumentID == documentID {
			found = append(found, chunk)
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].place < found[b].place })
	return found
}

func (l *library) ChunkCount(_ context.Context, documentID int64, userID string) (int64, error) {
	return int64(len(l.passages(documentID, userID))), nil
}

func (l *library) ChunksBefore(_ context.Context, cited Cited, userID string) (int64, error) {
	var before int64
	for _, chunk := range l.passages(cited.DocumentID, userID) {
		if chunk.place < cited.Position {
			before++
		}
	}
	return before, nil
}

func (l *library) ChunkPage(_ context.Context, documentID int64, userID string, limit, offset int64) ([]Passage, error) {
	var page []Passage
	for index, chunk := range l.passages(documentID, userID) {
		if int64(index) >= offset && int64(len(page)) < limit {
			page = append(page, chunk.passage)
		}
	}
	return page, nil
}

// Versions and Version take no account, as the statements behind them take
// none: the route must have asked Owns first, and the isolation test fails
// if it did not.
func (l *library) Versions(_ context.Context, documentID int64) ([]VersionSummary, error) {
	var listed []VersionSummary
	for _, version := range l.versions[documentID] {
		listed = append(listed, VersionSummary{Number: version.Number, Title: version.Title,
			ContentHash: version.ContentHash, CreatedAt: version.CreatedAt})
	}
	return listed, nil
}

func (l *library) Version(_ context.Context, documentID, number int64) (*Version, error) {
	for _, version := range l.versions[documentID] {
		if version.Number == number {
			return &version, nil
		}
	}
	return nil, nil
}

func (l *library) Restorable(_ context.Context, documentID int64, userID string) (*Restorable, error) {
	if !l.owns(documentID, userID) {
		return nil, nil
	}
	record := l.documents[documentID].record
	return &Restorable{ID: record.ID, UserID: userID, Title: record.Title, ContentHash: record.ContentHash}, nil
}

func (l *library) Restore(_ context.Context, work RestoreWork) error {
	l.restored = append(l.restored, work)
	return nil
}

func (l *library) Enqueue(_ context.Context, message produce.Message) (string, error) {
	l.queued = append(l.queued, message)
	return "task-1", nil
}

func (l *library) ProgressOf(_ context.Context, userID string, documentIDs []int64) ([]Progress, error) {
	l.asked = append(l.asked, userID)
	var progress []Progress
	for _, id := range documentIDs {
		if held, found := l.documents[id]; found && held.owner == userID {
			progress = append(progress, Progress{ID: id, Title: held.record.Title,
				DocumentType: held.record.DocumentType, Status: []byte(held.status)})
		}
	}
	sort.Slice(progress, func(a, b int) bool { return progress[a].ID < progress[b].ID })
	return progress, nil
}

func (l *library) TypeCountsOf(_ context.Context, userID string) ([]TypeCount, error) {
	l.asked = append(l.asked, userID)
	counted := map[string]int64{}
	for _, held := range l.documents {
		if held.owner == userID {
			counted[held.record.DocumentType]++
		}
	}
	var counts []TypeCount
	for kind, count := range counted {
		counts = append(counts, TypeCount{DocumentType: kind, Count: count})
	}
	sort.Slice(counts, func(a, b int) bool { return counts[a].DocumentType < counts[b].DocumentType })
	return counts, nil
}

func (l *library) WatchedFoldersOf(_ context.Context, userID string) ([]folders.Folder, error) {
	l.asked = append(l.asked, userID)
	return l.watched[userID], nil
}

func (l *library) titles(userID string, limit, offset int64, keep func(string) bool) []TitleRow {
	l.asked = append(l.asked, userID)
	var rows []TitleRow
	for id, held := range l.documents {
		if held.owner == userID && keep(held.record.Title) {
			rows = append(rows, TitleRow{ID: id, Title: held.record.Title, DocumentType: held.record.DocumentType})
		}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].ID < rows[b].ID })
	if offset > int64(len(rows)) {
		return nil
	}
	rows = rows[offset:]
	if int64(len(rows)) > limit {
		rows = rows[:limit]
	}
	return rows
}

func (l *library) RecentTitles(_ context.Context, userID string, limit, offset int64) ([]TitleRow, error) {
	return l.titles(userID, limit, offset, func(string) bool { return true }), nil
}

func (l *library) MatchingTitles(_ context.Context, userID, term string, limit, offset int64) ([]TitleRow, error) {
	return l.titles(userID, limit, offset, func(title string) bool {
		return strings.Contains(strings.ToLower(title), strings.ToLower(term))
	}), nil
}

var day = time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC)

// newLibrary is the owner's two documents, four chunks, two versions and
// one watched folder. The stranger has nothing at all.
func newLibrary() *library {
	title := "Tide tables, first draft"
	old := "# Tides\n\nThe first draft."
	document := func(id int64, name, kind, status string) libraryDocument {
		return libraryDocument{owner: owner, status: status, record: Record{
			ID: id, Title: name, DocumentType: kind, Metadata: []byte(`{"pages": 2}`),
			Content: "text of " + name, ContentHash: "hash-" + name, CreatedAt: day,
		}}
	}
	chunk := func(id, documentID, place int64, text string) libraryChunk {
		return libraryChunk{owner: owner, place: place,
			passage: Passage{ID: id, DocumentID: documentID, Content: text, CreatedAt: day}}
	}
	return &library{
		documents: map[int64]libraryDocument{
			41: document(41, "Tide tables", "FILE", `{"state": "ready"}`),
			42: document(42, "Shore notes", "NOTE", `{"state": "processing", "stage": "embedding"}`),
		},
		chunks: map[int64]libraryChunk{
			501: chunk(501, 41, 0, "The spring tide peaks after the full moon."),
			502: chunk(502, 41, 1, "The neap tide follows the quarter moon."),
			503: chunk(503, 41, 2, "A limpet returns to its scar."),
			504: chunk(504, 42, 0, "Rockpool notes."),
		},
		versions: map[int64][]Version{41: {
			{Number: 2, Title: &title, ContentHash: "h2", SourceMarkdown: &old, CreatedAt: day},
			{Number: 1, ContentHash: "h1", CreatedAt: day.Add(-time.Hour)},
		}},
		watched: map[string][]folders.Folder{owner: {{
			ID: 7, Name: "Shore", Position: "a0", CreatedAt: day, UpdatedAt: day, Metadata: []byte(`{"watched": true}`),
		}}},
	}
}

// mount registers the nine routes cmd/api serves on one API, over one
// library.
func mount(held *library) huma.API {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	served := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	ChunksRoute{Chunks: held, Logger: logger}.Register(served)
	VersionsRoute{Versions: held, Logger: logger}.Register(served)
	VersionRoute{Versions: held, Logger: logger}.Register(served)
	RestoreRoute{Restores: held, Queue: held, Logger: logger, Now: func() time.Time { return day }}.Register(served)
	StatusRoute{Statuses: held, Logger: logger}.Register(served)
	TypeCountsRoute{Counts: held, Logger: logger}.Register(served)
	WatchedFoldersRoute{Folders: held, Logger: logger}.Register(served)
	TitlesRoute{Titles: held, Logger: logger}.Register(served)
	ByChunkRoute{Chunks: held, Logger: logger}.Register(served)
	return served
}

// ask sends one request as one account.
func ask(t *testing.T, held *library, caller, method, target string) (int, string) {
	t.Helper()
	api := mount(held)
	request := httptest.NewRequest(method, target, nil)
	request = request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: caller, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// decode reads an answer as JSON, whatever its shape.
func decode(t *testing.T, body string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, body)
	}
	return value
}

func TestEachRouteAnswersTheOwner(t *testing.T) {
	for _, test := range []struct {
		name, method, target string
		// says is text the answer must hold, each piece of it.
		says []string
	}{
		{"a page of chunks, in reading order", http.MethodGet, "/api/v1/documents/41/chunks?page_size=2",
			[]string{`"total":3`, `"has_more":true`, `"id":501`, `"id":502`}},
		{"the page after it", http.MethodGet, "/api/v1/documents/41/chunks?page_size=2&page=1",
			[]string{`"total":3`, `"has_more":false`, `"id":503`}},
		{"the document a citation points into", http.MethodGet, "/api/v1/chunks/502/document?chunk_window=1",
			[]string{`"id":41`, `"title":"Tide tables"`, `"total_chunks":3`, `"chunk_start_index":0`, `"id":501`, `"id":503`}},
		{"the history, newest first", http.MethodGet, "/api/v1/documents/41/versions",
			[]string{`"version_number":2`, `"version_number":1`, `"title":"Tide tables, first draft"`}},
		{"one version, with its text", http.MethodGet, "/api/v1/documents/41/versions/2",
			[]string{`"version_number":2`, `"source_markdown":"# Tides\n\nThe first draft."`}},
		{"the restore", http.MethodPost, "/api/v1/documents/41/versions/2/restore",
			[]string{`"document_id":41`, `"restored_version":2`}},
		{"the status poll", http.MethodGet, "/api/v1/documents/status?document_ids=42,41",
			[]string{`"id":41`, `"state":"ready"`, `"id":42`, `"state":"processing"`, `"stage":"embedding"`}},
		{"the counts by type", http.MethodGet, "/api/v1/documents/type-counts", []string{`"FILE":1`, `"NOTE":1`}},
		{"the watched folders", http.MethodGet, "/api/v1/documents/watched-folders", []string{`"id":7`, `"name":"Shore"`, `"watched":true`}},
		{"the title search, with no term", http.MethodGet, "/api/v1/documents/search/titles", []string{`"id":41`, `"id":42`, `"has_more":false`}},
		{"the title search, with one", http.MethodGet, "/api/v1/documents/search/titles?title=shore", []string{`"id":42`, `"title":"Shore notes"`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			held := newLibrary()
			status, body := ask(t, held, owner, test.method, test.target)
			if status != http.StatusOK {
				t.Fatalf("answered %d %s", status, body)
			}
			for _, piece := range test.says {
				// JSON escapes a newline; compare against the encoded form.
				encoded := strings.ReplaceAll(piece, "\n", `\n`)
				if !strings.Contains(body, encoded) {
					t.Errorf("the answer does not hold %s:\n%s", encoded, body)
				}
			}
			// No answer of this package says whose a row is: the caller
			// already knows, and there is no other owner to name.
			for _, key := range []string{"workspace_id", "user_id", "created_by_id"} {
				if strings.Contains(body, `"`+key+`"`) {
					t.Errorf("the answer carries %s: %s", key, body)
				}
			}
		})
	}
}

// The isolation rule. Account B asks every route about account A's rows.
// A route about one document answers the 404 an id that was never used
// gets, word for word; a route about all of an account's documents answers
// an empty collection; and nothing is written or queued.
func TestAnotherAccountSeesAndChangesNothing(t *testing.T) {
	for _, test := range []struct {
		name, method, target string
		// unused is the same request for an id nobody has; "" for a
		// collection, which is compared with `empty` instead.
		unused string
		empty  string
	}{
		{name: "the chunk page", method: http.MethodGet, target: "/api/v1/documents/41/chunks", unused: "/api/v1/documents/9041/chunks"},
		{name: "the cited chunk's document", method: http.MethodGet, target: "/api/v1/chunks/502/document", unused: "/api/v1/chunks/502/document"},
		{name: "the history", method: http.MethodGet, target: "/api/v1/documents/41/versions", unused: "/api/v1/documents/9041/versions"},
		{name: "one version", method: http.MethodGet, target: "/api/v1/documents/41/versions/2", unused: "/api/v1/documents/9041/versions/2"},
		{name: "the restore", method: http.MethodPost, target: "/api/v1/documents/41/versions/2/restore", unused: "/api/v1/documents/9041/versions/2/restore"},
		{name: "the status poll", method: http.MethodGet, target: "/api/v1/documents/status?document_ids=41,42", empty: `{"items":[]}`},
		{name: "the counts by type", method: http.MethodGet, target: "/api/v1/documents/type-counts", empty: `{}`},
		{name: "the watched folders", method: http.MethodGet, target: "/api/v1/documents/watched-folders", empty: `[]`},
		{name: "the title search", method: http.MethodGet, target: "/api/v1/documents/search/titles", empty: `{"items":[],"has_more":false}`},
		{name: "the title search for a title the owner has", method: http.MethodGet, target: "/api/v1/documents/search/titles?title=tide", empty: `{"items":[],"has_more":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			held := newLibrary()
			status, body := ask(t, held, stranger, test.method, test.target)

			if test.unused == "" {
				if status != http.StatusOK || body != test.empty {
					t.Errorf("answered %d %s, want 200 %s", status, body, test.empty)
				}
			} else {
				if status != http.StatusNotFound {
					t.Fatalf("answered %d %s, want 404", status, body)
				}
				// What an id nobody has is told. The by-chunk route names
				// the chunk id in its 404, so there the comparison is with
				// an empty library asked for the very same id.
				unusedIn := held
				if test.unused == test.target {
					unusedIn = &library{}
				}
				_, absent := ask(t, unusedIn, stranger, test.method, test.unused)
				if message(t, body) != message(t, absent) {
					t.Errorf("another account's row is told %q and an unused id %q; the two must not differ",
						message(t, body), message(t, absent))
				}
			}
			if len(held.restored) != 0 || len(held.queued) != 0 {
				t.Errorf("another account's request restored %d and queued %d", len(held.restored), len(held.queued))
			}
			// Every statement was issued for the caller, never for the
			// owner of the row asked about.
			if len(held.asked) == 0 {
				t.Error("no statement carried an account at all")
			}
			for _, account := range held.asked {
				if account != stranger {
					t.Errorf("a statement was issued for %q, and the caller is %q", account, stranger)
				}
			}
		})
	}
}

// message is the sentence of an error envelope.
func message(t *testing.T, body string) string {
	t.Helper()
	envelope, _ := decode(t, body).(map[string]any)
	inner, _ := envelope["error"].(map[string]any)
	text, _ := inner["message"].(string)
	if text == "" {
		t.Fatalf("not an error envelope: %s", body)
	}
	return text
}

// The owner is the caller and nothing in a request can say otherwise: a
// query string naming the owner's account, or the workspace parameter the
// routes used to require, changes no answer.
func TestNothingInARequestNamesAnOwner(t *testing.T) {
	for _, target := range []string{
		"/api/v1/documents/status?document_ids=41",
		"/api/v1/documents/type-counts",
		"/api/v1/documents/watched-folders",
		"/api/v1/documents/search/titles",
	} {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		_, plain := ask(t, newLibrary(), stranger, http.MethodGet, target)
		for _, extra := range []string{"workspace_id=7", "user_id=" + owner, "owner=" + owner} {
			status, body := ask(t, newLibrary(), stranger, http.MethodGet, target+separator+extra)
			if status != http.StatusOK || body != plain {
				t.Errorf("%s with %s answered %d %s, want what it answers without: %s", target, extra, status, body, plain)
			}
		}
	}
}

func TestTheRestoreWritesForTheOwnerAndQueuesTheReindex(t *testing.T) {
	held := newLibrary()
	if status, body := ask(t, held, owner, http.MethodPost, "/api/v1/documents/41/versions/2/restore"); status != http.StatusOK {
		t.Fatalf("answered %d %s", status, body)
	}
	if len(held.restored) != 1 {
		t.Fatalf("%d restores were written", len(held.restored))
	}
	// The write names the owner the document was read for.
	work := held.restored[0]
	if work.Document.ID != 41 || work.Document.UserID != owner || work.Version.Number != 2 || !work.Now.Equal(day) {
		t.Errorf("restored %+v", work)
	}
	// After the write: the task reads the row it is about.
	if len(held.queued) != 1 {
		t.Fatalf("%d messages were queued", len(held.queued))
	}
	queued := held.queued[0]
	// The indexer's own job, on its own queue: the Python reindex task is
	// gone.
	if queued.Task != indexqueue.Task || queued.Queue != indexqueue.Queue || len(queued.Args) != 1 || queued.Args[0] != int64(41) {
		t.Errorf("queued %s%v on %q", queued.Task, queued.Args, queued.Queue)
	}
	if queued.Context.UserID != owner {
		t.Errorf("the message's log context names %q", queued.Context.UserID)
	}
}

func TestWhatTheRoutesRefuse(t *testing.T) {
	for _, test := range []struct {
		name, method, target string
		status               int
		says                 string
	}{
		{"a document id that is not a number", http.MethodGet, "/api/v1/documents/x/chunks", 422, "document_id"},
		{"a page size past the largest", http.MethodGet, "/api/v1/documents/41/chunks?page_size=101", 422, atMostOneHundred},
		{"a version the document does not have", http.MethodGet, "/api/v1/documents/41/versions/9", 404, versionAbsent},
		{"restoring a version the document does not have", http.MethodPost, "/api/v1/documents/41/versions/9/restore", 404, versionAbsent},
		{"a status poll with no list", http.MethodGet, "/api/v1/documents/status", 422, "document_ids"},
		{"a status poll with a piece that is not a number", http.MethodGet, "/api/v1/documents/status?document_ids=41,forty", 400, "Invalid document id: forty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			held := newLibrary()
			status, body := ask(t, held, owner, test.method, test.target)
			if status != test.status || !strings.Contains(body, test.says) {
				t.Errorf("answered %d %s, want %d saying %q", status, body, test.status, test.says)
			}
			if len(held.restored) != 0 || len(held.queued) != 0 {
				t.Errorf("a refused request restored %d and queued %d", len(held.restored), len(held.queued))
			}
		})
	}
}

// A status poll with an empty list asks the database nothing.
func TestAnEmptyStatusPollIssuesNoStatement(t *testing.T) {
	held := newLibrary()
	status, body := ask(t, held, owner, http.MethodGet, "/api/v1/documents/status?document_ids=")
	if status != http.StatusOK || body != `{"items":[]}` {
		t.Fatalf("answered %d %s", status, body)
	}
	if len(held.asked) != 0 {
		t.Errorf("%d statements for an empty list", len(held.asked))
	}
}
