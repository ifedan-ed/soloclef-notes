package drivesync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivefile"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// databaseVariable names a throwaway Postgres for this package's tests.
// **Every account in it is deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_DRIVESYNC_DATABASE"

// queue records what was published, and can be down.
type queue struct {
	sent []produce.Message
	down bool
}

func (q *queue) Enqueue(_ context.Context, message produce.Message) (string, error) {
	if q.down {
		return "", errors.New("the broker is down")
	}
	q.sent = append(q.sent, message)
	return "id", nil
}

// requests is what the published jobs ask for, read as the worker reads
// them: through JSON.
func (q *queue) requests(t *testing.T) []drivesyncjob.Request {
	t.Helper()
	var read []drivesyncjob.Request
	for _, message := range q.sent {
		wire, _ := json.Marshal(message.Kwargs)
		var kwargs map[string]any
		_ = json.Unmarshal(wire, &kwargs)
		request, err := drivesyncjob.Read(kwargs)
		if err != nil || message.Task != drivesyncjob.Task || !message.IgnoreResult || len(message.Args) != 0 {
			t.Fatalf("published %+v, which the worker would not read (%v)", message, err)
		}
		read = append(read, request)
	}
	return read
}

type driveSite struct {
	t       *testing.T
	pool    *pgxpool.Pool
	queue   *queue
	handler http.Handler
	// caller is who is asking.
	caller string
}

// The rows every test starts from: Alice's Drive connection (1), Bob's
// (2), Alice's web-pages connection (3), and a notebook each (10, 20).
func newDriveSite(t *testing.T, configured bool) *driveSite {
	t.Helper()
	s := &driveSite{t: t, pool: testpostgres.Open(t, databaseVariable), queue: &queue{}, caller: testpostgres.Alice}
	testpostgres.Exec(t, s.pool, `
INSERT INTO search_source_connectors (id, user_id, name, connector_type, is_indexable, config) VALUES
 (1, '`+testpostgres.Alice+`', 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', true, '{"refresh_token": "sealed", "auth_expired": false}'),
 (2, '`+testpostgres.Bob+`', 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', true, '{"refresh_token": "sealed", "auth_expired": false}'),
 (3, '`+testpostgres.Alice+`', 'Web pages', 'WEBCRAWLER_CONNECTOR', true, '{}');
INSERT INTO notebooks (id, user_id, title) VALUES
 (10, '`+testpostgres.Alice+`', 'Alice''s'), (20, '`+testpostgres.Bob+`', 'Bob''s')`)
	routes := Routes{
		Rows: Store{Pool: s.pool}, Queue: s.queue, Configured: configured,
		// Local is the default and reads .pdf and .docx; Datalab is
		// configured and also reads .png; the other two are not.
		Parsers: parserchoicetest.Registry(t, parse.Local, parse.Datalab),
		Errors:  errorenvelope.Writer{ReportURL: "https://notes.example/contact"},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	s.handler = api.Adapter()
	return s
}

type answer struct {
	status        int
	body          string
	code, message string
}

func (s *driveSite) post(path, body string) answer {
	s.t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: s.caller, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	got := answer{status: recorder.Code, body: recorder.Body.String()}
	if recorder.Code != http.StatusOK {
		var envelope struct {
			Error struct {
				Code, Message string
				Status        int
			}
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Status != recorder.Code {
			s.t.Fatalf("a refusal that is not the error envelope: %d %s", recorder.Code, recorder.Body)
		}
		got.code, got.message = envelope.Error.Code, envelope.Error.Message
	}
	return got
}

// imported is the import's answer.
type imported struct {
	DocumentIDs []int64  `json:"document_ids"`
	Queued      int      `json:"queued"`
	Unsupported []string `json:"unsupported"`
}

func (s *driveSite) imports(path, body string) imported {
	s.t.Helper()
	got := s.post(path, body)
	if got.status != http.StatusOK {
		s.t.Fatalf("the import answered %d %s", got.status, got.body)
	}
	var read imported
	if err := json.Unmarshal([]byte(got.body), &read); err != nil || read.DocumentIDs == nil || read.Unsupported == nil {
		s.t.Fatalf("the import answered %s (%v)", got.body, err)
	}
	return read
}

// one is one value from the database.
func one[T any](t *testing.T, pool *pgxpool.Pool, query string, arguments ...any) T {
	t.Helper()
	var value T
	if err := pool.QueryRow(context.Background(), query, arguments...).Scan(&value); err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	return value
}

const (
	reportID = "1ReportFileIdFromDrive"
	planID   = "2PlanFileIdFromDrive00"
	photoID  = "3PhotoFileIdFromDrive0"
	folderID = "4FolderIdFromDrive0000"

	importAlices = "/api/v1/connectors/1/google-drive/import"
	indexAlices  = "/api/v1/search-source-connectors/1/index"
	picks        = `{"files": [
		{"id": "` + reportID + `", "name": "Report.pdf", "mime_type": "application/pdf"},
		{"id": "` + planID + `", "name": "Plan", "mime_type": "application/vnd.google-apps.document"},
		{"id": "` + photoID + `", "name": "Photo.png", "mime_type": "image/png"},
		{"id": "` + folderID + `", "name": "A folder", "mime_type": "application/vnd.google-apps.folder"}
	], "notebook_id": 10}`
)

// An import makes a waiting document per importable pick, links the
// notebook, says which picks it left out, and publishes one job naming the
// files. Nothing asks Google.
func TestAnImportMakesTheDocumentsAndPublishesTheJob(t *testing.T) {
	s := newDriveSite(t, true)
	got := s.imports(importAlices, picks)
	// The Doc is importable because it will be stored as Plan.docx; the
	// picture is not read by this connection's parser; a folder is no file.
	if got.Queued != 2 || len(got.DocumentIDs) != 2 || !reflect.DeepEqual(got.Unsupported, []string{"Photo.png", "A folder"}) {
		t.Fatalf("answered %+v", got)
	}
	for index, want := range []struct{ fileID, title string }{{reportID, "Report.pdf"}, {planID, "Plan"}} {
		id := got.DocumentIDs[index]
		row := one[string](t, s.pool, `
			SELECT concat_ws('|', title, document_type::text, user_id::text, connector_id::text, status ->> 'state', status ->> 'stage',
			       COALESCE(parser, 'none'), unique_identifier_hash, document_metadata #>> '{google_drive,file_id}', content)
			FROM documents WHERE id = $1`, id)
		expected := strings.Join([]string{want.title, "GOOGLE_DRIVE_FILE", testpostgres.Alice, "1", "pending", "downloading",
			"none", drivefile.Identity(want.fileID, testpostgres.Alice), want.fileID, ""}, "|")
		if row != expected {
			t.Errorf("document %d is\n %s\nwant\n %s", id, row, expected)
		}
		if linked := one[int](t, s.pool, `SELECT count(*) FROM notebook_sources WHERE notebook_id = 10 AND document_id = $1 AND enabled`, id); linked != 1 {
			t.Errorf("document %d is not in the notebook", id)
		}
	}
	want := []drivesyncjob.Request{{ConnectorID: 1, UserID: testpostgres.Alice, FileIDs: []string{reportID, planID}}}
	if published := s.queue.requests(t); !reflect.DeepEqual(published, want) {
		t.Errorf("published %+v", published)
	}

	// The same picks again: the same documents, no second copy, and the
	// job published again so the worker can see whether a file changed.
	again := s.imports(importAlices, picks)
	if !reflect.DeepEqual(again.DocumentIDs, got.DocumentIDs) || one[int](t, s.pool, `SELECT count(*) FROM documents`) != 2 || len(s.queue.sent) != 2 {
		t.Errorf("a second import answered %+v", again)
	}
	// A ready document is left ready; a failed one is owed again.
	testpostgres.Exec(t, s.pool, `UPDATE documents SET status = '{"state": "ready"}' WHERE document_metadata #>> '{google_drive,file_id}' = '`+reportID+`';
		UPDATE documents SET status = '{"state": "failed", "reason": "x"}' WHERE document_metadata #>> '{google_drive,file_id}' = '`+planID+`'`)
	s.imports(importAlices, picks)
	states := one[string](t, s.pool, `SELECT string_agg(status ->> 'state' || ':' || COALESCE(status ->> 'stage', ''), ',' ORDER BY id) FROM documents`)
	if states != "ready:,pending:downloading" {
		t.Errorf("after a third import the documents are %s", states)
	}
}

// The connection's own parser decides what is importable.
func TestTheConnectionsParserDecidesWhatIsImportable(t *testing.T) {
	s := newDriveSite(t, true)
	testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors SET parser = 'datalab' WHERE id = 1`)
	if got := s.imports(importAlices, picks); got.Queued != 3 || !reflect.DeepEqual(got.Unsupported, []string{"A folder"}) {
		t.Errorf("with a parser that reads pictures: %+v", got)
	}
	// A parser this server no longer has configured is refused by name,
	// and nothing is written.
	testpostgres.Exec(t, s.pool, `DELETE FROM documents; UPDATE search_source_connectors SET parser = 'llamaparse' WHERE id = 1`)
	got := s.post(importAlices, picks)
	if got.status != http.StatusBadRequest || !strings.Contains(got.message, "llamaparse") || one[int](t, s.pool, `SELECT count(*) FROM documents`) != 0 {
		t.Errorf("with a parser that is not configured: %d %s", got.status, got.body)
	}
	// Nothing importable is not an error, and publishes nothing.
	testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors SET parser = NULL WHERE id = 1`)
	sent := len(s.queue.sent)
	none := s.imports(importAlices, `{"files": [{"id": "`+photoID+`", "name": "Photo.png", "mime_type": "image/png"}], "notebook_id": null}`)
	if none.Queued != 0 || len(none.DocumentIDs) != 0 || len(s.queue.sent) != sent {
		t.Errorf("an import of nothing importable: %+v", none)
	}
}

// What the import refuses, and that a refusal writes nothing.
func TestWhatAnImportRefuses(t *testing.T) {
	s := newDriveSite(t, true)
	for name, tc := range map[string]struct {
		path, body string
		status     int
		code       string
	}{
		"a body that is not picks":        {importAlices, `{"files": []}`, http.StatusUnprocessableEntity, ""},
		"an id that is not a number":      {"/api/v1/connectors/abc/google-drive/import", picks, http.StatusUnprocessableEntity, ""},
		"an id no connection has":         {"/api/v1/connectors/999/google-drive/import", picks, http.StatusNotFound, ""},
		"a connection that is not Drive":  {"/api/v1/connectors/3/google-drive/import", picks, http.StatusNotFound, ""},
		"a notebook that does not exist":  {importAlices, strings.Replace(picks, `"notebook_id": 10`, `"notebook_id": 99`, 1), http.StatusNotFound, ""},
		"a notebook of another account's": {importAlices, strings.Replace(picks, `"notebook_id": 10`, `"notebook_id": 20`, 1), http.StatusNotFound, ""},
	} {
		got := s.post(tc.path, tc.body)
		if got.status != tc.status {
			t.Errorf("%s: answered %d %s", name, got.status, got.body)
		}
	}
	// Google has refused the grant: the code the web offers "connect
	// again" for, and never a 401.
	testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors SET config = config || '{"auth_expired": true}' WHERE id = 1`)
	for _, path := range []string{importAlices, indexAlices} {
		if got := s.post(path, picks); got.status != http.StatusBadRequest || got.code != reconnectCode {
			t.Errorf("%s with a refused grant answered %d %s", path, got.status, got.body)
		}
	}
	if documents := one[int](t, s.pool, `SELECT count(*) FROM documents`); documents != 0 || len(s.queue.sent) != 0 {
		t.Errorf("refusals wrote %d documents and published %d jobs", documents, len(s.queue.sent))
	}

	// A deployment with no Google project: 503, with its code, and nothing
	// written.
	unset := newDriveSite(t, false)
	for _, path := range []string{importAlices, indexAlices} {
		if got := unset.post(path, picks); got.status != http.StatusServiceUnavailable || got.code != notConfiguredCode {
			t.Errorf("%s on a deployment without Drive answered %d %s", path, got.status, got.body)
		}
	}
}

// The queue is down: the documents say so instead of waiting for ever, and
// the caller is told to try again.
func TestAnImportWhoseJobCannotBePublishedSaysSo(t *testing.T) {
	s := newDriveSite(t, true)
	s.queue.down = true
	got := s.post(importAlices, picks)
	if got.status != http.StatusServiceUnavailable {
		t.Fatalf("answered %d %s", got.status, got.body)
	}
	states := one[string](t, s.pool, `SELECT string_agg(DISTINCT (status ->> 'state') || ': ' || (status ->> 'reason'), ',') FROM documents`)
	if states != "failed: "+QueueingFailed {
		t.Errorf("the documents are %q", states)
	}
	if got := s.post(indexAlices, ``); got.status != http.StatusServiceUnavailable {
		t.Errorf("sync now with the queue down answered %d %s", got.status, got.body)
	}
	// Import again once the queue is back: the same documents, owed again.
	s.queue.down = false
	again := s.imports(importAlices, picks)
	if again.Queued != 2 || one[int](t, s.pool, `SELECT count(*) FROM documents WHERE status ->> 'stage' = 'downloading'`) != 2 {
		t.Errorf("the retry answered %+v", again)
	}
}

// "Sync now" publishes a sync of everything for a Drive connection, and
// says no for anything else.
func TestSyncNow(t *testing.T) {
	s := newDriveSite(t, true)
	got := s.post(indexAlices+"?start_date=2026-10-01&end_date=2026-11-01", ``)
	if got.status != http.StatusOK || got.body != `{"message":"`+syncStarted+`","connector_id":1}` {
		t.Fatalf("answered %d %s", got.status, got.body)
	}
	if published := s.queue.requests(t); !reflect.DeepEqual(published, []drivesyncjob.Request{{ConnectorID: 1, UserID: testpostgres.Alice}}) {
		t.Errorf("published %+v", published)
	}
	if got := s.post("/api/v1/search-source-connectors/3/index", ``); got.status != http.StatusBadRequest || got.message != nothingToSync {
		t.Errorf("a web-pages connection answered %d %s", got.status, got.body)
	}
	if got := s.post("/api/v1/search-source-connectors/999/index", ``); got.status != http.StatusNotFound {
		t.Errorf("an id no connection has answered %d %s", got.status, got.body)
	}
	testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors SET enabled = false WHERE id = 1`)
	if got := s.post(indexAlices, ``); got.status != http.StatusBadRequest || got.message != switchedOff {
		t.Errorf("a connection that is switched off answered %d %s", got.status, got.body)
	}
	if len(s.queue.sent) != 1 {
		t.Errorf("%d jobs were published; one sync was asked for", len(s.queue.sent))
	}
}

// Both routes are keyed by the connection's id AND the caller. Bob cannot
// import through, or sync, Alice's connection: he is told what he would be
// told about an id that names nothing, and nothing is written or
// published. The same Drive file is a document each.
func TestAnotherAccountsConnectionIsNotThere(t *testing.T) {
	s := newDriveSite(t, true)
	s.caller = testpostgres.Bob
	missing := s.post("/api/v1/connectors/999/google-drive/import", picks)
	got := s.post(importAlices, strings.Replace(picks, `"notebook_id": 10`, `"notebook_id": 20`, 1))
	if got.status != http.StatusNotFound || got.message != missing.message {
		t.Errorf("Bob importing through Alice's connection: %d %s", got.status, got.body)
	}
	absent := s.post("/api/v1/search-source-connectors/999/index", ``)
	if got := s.post(indexAlices, ``); got.status != http.StatusNotFound || got.message != absent.message {
		t.Errorf("Bob syncing Alice's connection: %d %s", got.status, got.body)
	}
	if documents := one[int](t, s.pool, `SELECT count(*) FROM documents`); documents != 0 || len(s.queue.sent) != 0 {
		t.Fatalf("Bob's refused requests wrote %d documents and published %d jobs", documents, len(s.queue.sent))
	}

	// Each imports the same file through their own connection: a document
	// each, each under its own account, connection and notebook.
	bobs := s.imports("/api/v1/connectors/2/google-drive/import", strings.Replace(picks, `"notebook_id": 10`, `"notebook_id": 20`, 1))
	s.caller = testpostgres.Alice
	alices := s.imports(importAlices, picks)
	if bobs.DocumentIDs[0] == alices.DocumentIDs[0] {
		t.Fatal("two accounts were given one document")
	}
	for _, check := range []struct {
		owner     string
		connector int
		notebook  int
		ids       []int64
	}{{testpostgres.Bob, 2, 20, bobs.DocumentIDs}, {testpostgres.Alice, 1, 10, alices.DocumentIDs}} {
		owned := one[int](t, s.pool, `SELECT count(*) FROM documents WHERE id = ANY($1) AND user_id = $2::uuid AND connector_id = $3`,
			check.ids, check.owner, check.connector)
		linked := one[int](t, s.pool, `SELECT count(*) FROM notebook_sources WHERE document_id = ANY($1) AND notebook_id = $2 AND user_id = $3::uuid`,
			check.ids, check.notebook, check.owner)
		if owned != 2 || linked != 2 {
			t.Errorf("%s has %d of the documents and %d links", check.owner, owned, linked)
		}
	}
	for _, request := range s.queue.requests(t) {
		if (request.ConnectorID == 1) != (request.UserID == testpostgres.Alice) {
			t.Errorf("a job names a connection and an account that do not go together: %+v", request)
		}
	}
}
