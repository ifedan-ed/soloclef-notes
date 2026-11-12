package drivesync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivefile"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

const secretKey = "a secret key made up for this test"

// indexed records the documents handed to the indexer.
type indexed struct {
	mu  sync.Mutex
	ids []int64
	// down makes every publish fail, as a broker that is unreachable does.
	down bool
}

func (i *indexed) Index(_ context.Context, documentID int64) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.down {
		return io.ErrClosedPipe
	}
	i.ids = append(i.ids, documentID)
	return nil
}

func (i *indexed) count(documentID int64) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	count := 0
	for _, id := range i.ids {
		if id == documentID {
			count++
		}
	}
	return count
}

// site is one account with a Drive connection, a stub Google, and the job.
type site struct {
	t       *testing.T
	pool    *pgxpool.Pool
	google  *stubGoogle
	stores  *filestore.Stores
	indexer *indexed
	job     *Job
	now     time.Time

	owner     string
	connector int64
}

func newSite(t *testing.T) *site {
	t.Helper()
	pool := testdatabase.Open(t)
	// The real clock: a renewed token's expiry is counted from the moment
	// Google answered, which is the real one.
	s := &site{t: t, pool: pool, google: newStubGoogle(t), indexer: &indexed{}, now: time.Now()}
	stores, err := filestore.OpenStores(filestore.Settings{Backend: filestore.LocalBackend, LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stores.Close() })
	s.stores = stores
	writer, err := stores.Writer()
	if err != nil {
		t.Fatal(err)
	}
	s.job = &Job{
		Rows:   Store{Pool: pool},
		Google: drivegrant.Client{ClientID: "project-1", ClientSecret: "project-secret", TokenURL: s.google.URL + "/token", HTTP: NewHTTP(5 * time.Second)},
		Files:  writer, Indexer: s.indexer,
		SecretKey: secretKey, DriveURL: s.google.URL + "/drive/v3", HTTP: NewHTTP(5 * time.Second),
		MaxFileBytes: 1 << 20, Concurrency: 3,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return s.now },
	}
	if err := s.job.Check(); err != nil {
		t.Fatal(err)
	}
	s.owner = testdatabase.Account(t, pool)
	s.connector = s.connect(s.owner, "refresh-1", nil)
	return s
}

// connect makes a Drive connection as the API's connect stores one: both
// tokens sealed, the access token already run out. extra is merged into
// the config.
func (s *site) connect(owner, refreshToken string, extra map[string]any) int64 {
	s.t.Helper()
	seal := func(plain string) string {
		sealed, err := storedtoken.Seal(secretKey, plain, s.now, rand.Reader)
		if err != nil {
			s.t.Fatal(err)
		}
		return sealed
	}
	config := map[string]any{
		"token": seal("an access token that ran out"), "refresh_token": seal(refreshToken), "_token_encrypted": true,
		"expiry": s.now.Add(-time.Hour).UTC().Format(drivegrant.ExpiryLayout), "account_email": "ada@gmail.test", "auth_expired": false,
	}
	for key, value := range extra {
		config[key] = value
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		s.t.Fatal(err)
	}
	var id int64
	if err := s.pool.QueryRow(context.Background(), `
		INSERT INTO search_source_connectors (user_id, name, connector_type, is_indexable, config,
		                                      periodic_indexing_enabled, indexing_frequency_minutes, next_scheduled_at)
		VALUES ($1::uuid, 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', true, $2::jsonb, true, 1440, now() - interval '1 minute')
		RETURNING id`, owner, encoded).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// picked makes the document the API's import route makes for a picked
// file: pending, waiting for its download.
func (s *site) picked(owner string, connector int64, fileID, name, mimeType string) int64 {
	s.t.Helper()
	identity := drivefile.Identity(fileID, owner)
	metadata, _ := json.Marshal(map[string]any{"FILE_NAME": name, "source": "google_drive",
		"google_drive": map[string]any{"connector_id": connector, "file_id": fileID, "name": name, "mime_type": mimeType}})
	var id int64
	if err := s.pool.QueryRow(context.Background(), `
		INSERT INTO documents (title, document_type, document_metadata, content, content_hash, unique_identifier_hash,
		                       user_id, connector_id, status, created_at, updated_at)
		VALUES ($1, 'GOOGLE_DRIVE_FILE', $2::jsonb, '', $3, $4, $5::uuid, $6,
		        '{"state": "pending", "stage": "downloading"}'::jsonb, now(), now())
		RETURNING id`, name, metadata, drivefile.PlaceholderHash(identity), identity, owner, connector).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// run runs one job for the site's owner and returns its error.
func (s *site) run(fileIDs ...string) error {
	return s.runFor(s.owner, s.connector, fileIDs...)
}

func (s *site) runFor(owner string, connector int64, fileIDs ...string) error {
	request := drivesyncjob.Request{ConnectorID: connector, UserID: owner, FileIDs: fileIDs}
	// Through JSON, as the queue carries it.
	wire, _ := json.Marshal(request.Kwargs())
	var kwargs map[string]any
	_ = json.Unmarshal(wire, &kwargs)
	return s.job.Run(context.Background(), &celery.Message{Task: Task, Kwargs: kwargs})
}

// document is what a test reads of a document.
type documentRow struct {
	Title, State, Stage, Reason string
	ContentHash                 string
	Name, MD5, Modified         string
	Files                       int
	NewestName, NewestKey       string
	// Gone is google_drive.gone_from_drive_at, "" while Drive has the file.
	Gone string
}

func (s *site) document(id int64) (documentRow, bool) {
	s.t.Helper()
	var row documentRow
	err := s.pool.QueryRow(context.Background(), `
		SELECT d.title, COALESCE(d.status ->> 'state', ''), COALESCE(d.status ->> 'stage', ''), COALESCE(d.status ->> 'reason', ''),
		       d.content_hash,
		       COALESCE(d.document_metadata #>> '{google_drive,name}', ''),
		       COALESCE(d.document_metadata #>> '{google_drive,md5_checksum}', ''),
		       COALESCE(d.document_metadata #>> '{google_drive,modified_time}', ''),
		       (SELECT count(*) FROM document_files f WHERE f.document_id = d.id),
		       COALESCE((SELECT original_filename FROM document_files f WHERE f.document_id = d.id ORDER BY id DESC LIMIT 1), ''),
		       COALESCE((SELECT storage_key FROM document_files f WHERE f.document_id = d.id ORDER BY id DESC LIMIT 1), ''),
		       COALESCE(d.document_metadata #>> '{google_drive,gone_from_drive_at}', '')
		FROM documents d WHERE d.id = $1`, id).Scan(&row.Title, &row.State, &row.Stage, &row.Reason, &row.ContentHash,
		&row.Name, &row.MD5, &row.Modified, &row.Files, &row.NewestName, &row.NewestKey, &row.Gone)
	if err != nil {
		return documentRow{}, false
	}
	return row, true
}

// stored is the bytes kept under a key.
func (s *site) stored(key string) string {
	s.t.Helper()
	store, err := s.stores.Named(filestore.LocalBackend)
	if err != nil {
		s.t.Fatal(err)
	}
	body, err := store.Open(context.Background(), key)
	if err != nil {
		s.t.Fatalf("opening %s: %v", key, err)
	}
	defer body.Close()
	content, _ := io.ReadAll(body)
	return string(content)
}

// config is the connection's config as stored.
func (s *site) config(connector int64) map[string]any {
	s.t.Helper()
	var raw []byte
	if err := s.pool.QueryRow(context.Background(), `SELECT config FROM search_source_connectors WHERE id = $1`, connector).Scan(&raw); err != nil {
		s.t.Fatal(err)
	}
	var config map[string]any
	_ = json.Unmarshal(raw, &config)
	return config
}

// indexedAsReady plays the indexer: the document becomes ready.
func (s *site) indexedAsReady(id int64) {
	testdatabase.Exec(s.t, s.pool, `UPDATE documents SET status = '{"state": "ready"}'::jsonb WHERE id = $1`, id)
}

func pdf(id, name, content, modified string) stubFile {
	sum := sha256.Sum256([]byte(content))
	return stubFile{meta: remoteFile{ID: id, Name: name, MimeType: "application/pdf", Modified: modified, MD5: hex.EncodeToString(sum[:16])},
		body: []byte(content)}
}

const (
	reportID = "file-report-0001"
	planID   = "file-plan-00002"
	otherID  = "file-other-0003"
)

// A picked file and a picked Google Doc are stored and handed to the
// indexer; the renewed token is kept, sealed, for the next job.
func TestPickedFilesAreStoredAndHandedToTheIndexer(t *testing.T) {
	s := newSite(t)
	s.google.put(pdf(reportID, "Report.pdf", "the report's bytes", "2026-11-01T10:00:00.000Z"))
	s.google.put(stubFile{meta: remoteFile{ID: planID, Name: "Plan", MimeType: drivefile.GoogleDoc, Modified: "2026-11-02T10:00:00.000Z"},
		exports: map[string][]byte{"application/vnd.openxmlformats-officedocument.wordprocessingml.document": []byte("the plan, as Word")}})
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")
	plan := s.picked(s.owner, s.connector, planID, "Plan", drivefile.GoogleDoc)

	if err := s.run(reportID, planID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.document(report)
	sum := sha256.Sum256([]byte("the report's bytes"))
	if got.State != "pending" || got.Stage != "" || got.Files != 1 || got.NewestName != "Report.pdf" ||
		got.ContentHash != hex.EncodeToString(sum[:]) || got.MD5 == "" || s.stored(got.NewestKey) != "the report's bytes" {
		t.Errorf("the PDF's document is %+v", got)
	}
	if !strings.HasPrefix(got.NewestKey, "documents/"+s.owner+"/") {
		t.Errorf("the file is stored outside its owner's prefix: %s", got.NewestKey)
	}
	// The Doc is exported, and stored under a name its parser is chosen by.
	got, _ = s.document(plan)
	if got.State != "pending" || got.NewestName != "Plan.docx" || got.Title != "Plan" || got.Modified != "2026-11-02T10:00:00.000Z" ||
		s.stored(got.NewestKey) != "the plan, as Word" {
		t.Errorf("the Doc's document is %+v", got)
	}
	if s.indexer.count(report) != 1 || s.indexer.count(plan) != 1 {
		t.Errorf("the indexer was handed %v", s.indexer.ids)
	}

	// The renewed token is stored sealed, and good for the next job.
	config := s.config(s.connector)
	sealed, _ := config["token"].(string)
	if opened, err := storedtoken.Open(secretKey, sealed); err != nil || opened != accessTokenFor("refresh-1") {
		t.Errorf("the stored access token opens as %q (%v)", opened, err)
	}
	if strings.Contains(sealed, "access-for") || config["refresh_token"] == nil || config["account_email"] != "ada@gmail.test" {
		t.Errorf("the config after a renewal is %v", config)
	}
	s.indexedAsReady(report)
	if err := s.run(reportID); err != nil {
		t.Fatal(err)
	}
	if s.google.refreshes != 1 {
		t.Errorf("Google was asked for a token %d times; the stored one was good", s.google.refreshes)
	}
	// Picked again and unchanged: nothing is downloaded twice.
	if got, _ := s.document(report); got.Files != 1 || got.State != "ready" || s.indexer.count(report) != 1 {
		t.Errorf("an unchanged file picked again became %+v", got)
	}
	// A named job does not take a place in the change feed: only a sync of
	// everything knows that every file has been checked.
	if _, kept := s.config(s.connector)["start_page_token"]; kept {
		t.Error("an import stored a feed position")
	}
}

// The first sync of everything has no place in the feed: it takes one,
// checks every file, and keeps the place. Later syncs read the feed.
func TestASyncFollowsTheChangeFeed(t *testing.T) {
	s := newSite(t)
	s.google.put(pdf(reportID, "Report.pdf", "version one", "2026-11-01T10:00:00.000Z"))
	s.google.put(pdf(planID, "Plan.pdf", "the plan", "2026-11-01T11:00:00.000Z"))
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")
	plan := s.picked(s.owner, s.connector, planID, "Plan.pdf", "application/pdf")
	if err := s.run(reportID, planID); err != nil {
		t.Fatal(err)
	}
	s.indexedAsReady(report)
	s.indexedAsReady(plan)

	// The first sync: no position, so every file is asked about once.
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if position := s.config(s.connector)["start_page_token"]; position != "p2" {
		t.Fatalf("the first sync kept the position %v", position)
	}
	if got, _ := s.document(report); got.Files != 1 || got.State != "ready" {
		t.Errorf("the first sync fetched a file that had not changed: %+v", got)
	}
	var stamped bool
	_ = s.pool.QueryRow(context.Background(), `SELECT last_indexed_at IS NOT NULL FROM search_source_connectors WHERE id = $1`, s.connector).Scan(&stamped)
	if !stamped {
		t.Error("a finished sync did not stamp the connection")
	}

	// Nothing changed: one request, to the feed.
	before := len(s.google.requests)
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if asked := s.google.requests[before:]; len(asked) != 1 || asked[0] != "/changes" {
		t.Errorf("a sync with nothing to do asked Drive %v", asked)
	}

	// The report changes twice, the plan is renamed, a file nobody
	// imported changes, and a third page of the feed is needed for it all.
	s.google.put(pdf(reportID, "Report.pdf", "version two", "2026-11-03T10:00:00.000Z"))
	s.google.put(pdf(reportID, "Report.pdf", "version three", "2026-11-04T10:00:00.000Z"))
	s.google.put(pdf(planID, "Plan (final).pdf", "the plan", "2026-11-04T11:00:00.000Z"))
	s.google.put(pdf(otherID, "Somebody else's.pdf", "not imported", "2026-11-04T12:00:00.000Z"))
	downloads := s.google.seen("/files/")
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.document(report)
	if got.Files != 2 || got.State != "pending" || s.stored(got.NewestKey) != "version three" || s.indexer.count(report) != 2 {
		t.Errorf("a changed file became %+v, indexed %d times", got, s.indexer.count(report))
	}
	// Changed twice, fetched once; the file nobody imported not at all.
	if fetched := s.google.seen("/files/") - downloads; fetched != 1 {
		t.Errorf("%d requests for files; one file's content changed", fetched)
	}
	got, _ = s.document(plan)
	if got.Title != "Plan (final).pdf" || got.Name != "Plan (final).pdf" || got.Files != 1 || got.State != "ready" {
		t.Errorf("a renamed file became %+v", got)
	}
	if position := s.config(s.connector)["start_page_token"]; position != "p6" {
		t.Errorf("the position after the sync is %v", position)
	}

	// A title the person wrote is theirs: a later rename in Drive is
	// recorded and the title kept.
	testdatabase.Exec(t, s.pool, `UPDATE documents SET title = 'My own title' WHERE id = $1`, plan)
	s.google.put(pdf(planID, "Plan (really final).pdf", "the plan", "2026-11-05T11:00:00.000Z"))
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.document(plan); got.Title != "My own title" || got.Name != "Plan (really final).pdf" {
		t.Errorf("a renamed file whose document was retitled became %+v", got)
	}

	// Binned or removed in Drive: the document stays, as it was, with its
	// stored file, and says since when its file is gone.
	s.indexedAsReady(report)
	kept, _ := s.document(report)
	keptPlan, _ := s.document(plan)
	binned := pdf(reportID, "Report.pdf", "version three", "2026-11-04T10:00:00.000Z")
	binned.meta.Trashed = true
	s.google.put(binned)
	s.google.remove(planID)
	handedOn := s.indexer.count(report)
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	wantGone := s.now.UTC().Format(time.RFC3339)
	got, there := s.document(report)
	if !there || got.State != "ready" || got.Files != kept.Files || got.NewestKey != kept.NewestKey ||
		got.ContentHash != kept.ContentHash || got.Gone != wantGone {
		t.Errorf("a file binned in Drive left its document as %+v (was %+v)", got, kept)
	}
	if got, there := s.document(plan); !there || got.Title != keptPlan.Title || got.Files != keptPlan.Files || got.Gone != wantGone {
		t.Errorf("a file removed from Drive left its document as %+v (was %+v)", got, keptPlan)
	}
	store, _ := s.stores.Named(filestore.LocalBackend)
	if _, err := store.Stat(context.Background(), kept.NewestKey); err != nil {
		t.Errorf("a document whose file left Drive lost its stored file: %v", err)
	}
	if s.indexer.count(report) != handedOn {
		t.Errorf("a binned file was handed to the indexer: %v", s.indexer.ids)
	}

	// No longer synced: checking everything again asks Drive nothing
	// about either file, and the first moment it went is kept.
	testdatabase.Exec(t, s.pool, `UPDATE search_source_connectors SET config = config - 'start_page_token' WHERE id = $1`, s.connector)
	asked := s.google.seen("/files/")
	s.now = s.now.Add(time.Hour)
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if more := s.google.seen("/files/") - asked; more != 0 {
		t.Errorf("a sync of everything asked Drive %d times about files that are gone", more)
	}
	if got, _ := s.document(report); got.Gone != wantGone {
		t.Errorf("a gone file's moment became %q, want %q", got.Gone, wantGone)
	}

	// Restored from Drive's bin: the document stops saying it is gone,
	// and the content it has now is fetched.
	s.google.put(pdf(reportID, "Report.pdf", "version four", "2026-11-06T10:00:00.000Z"))
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.document(report); got.Gone != "" || s.stored(got.NewestKey) != "version four" {
		t.Errorf("a file restored in Drive left its document as %+v", got)
	}
}

// A document the person deleted here is not brought back by a later
// change to its file.
func TestADeletedDocumentStaysDeleted(t *testing.T) {
	s := newSite(t)
	s.google.put(pdf(reportID, "Report.pdf", "version one", "2026-11-01T10:00:00.000Z"))
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")
	if err := s.run(reportID); err != nil {
		t.Fatal(err)
	}
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	testdatabase.Exec(t, s.pool, `DELETE FROM documents WHERE id = $1`, report)
	s.google.put(pdf(reportID, "Report.pdf", "version two", "2026-11-03T10:00:00.000Z"))
	downloads := s.google.seen("/files/")
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	var documents int
	_ = s.pool.QueryRow(context.Background(), `SELECT count(*) FROM documents WHERE user_id = $1::uuid`, s.owner).Scan(&documents)
	if documents != 0 || s.google.seen("/files/") != downloads {
		t.Errorf("a deleted document's file was fetched again (%d documents)", documents)
	}
}

// Drive forgot the place: the sync takes a new one and checks every file,
// so nothing that changed meanwhile is missed.
func TestAForgottenFeedPositionIsReplacedByCheckingEveryFile(t *testing.T) {
	s := newSite(t)
	s.google.put(pdf(reportID, "Report.pdf", "version one", "2026-11-01T10:00:00.000Z"))
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")
	if err := s.run(reportID); err != nil {
		t.Fatal(err)
	}
	s.indexedAsReady(report)
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	s.google.put(pdf(reportID, "Report.pdf", "version two", "2026-11-03T10:00:00.000Z"))
	s.google.forgotten["p1"] = true

	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.document(report); s.stored(got.NewestKey) != "version two" {
		t.Errorf("the change made while the position was lost was missed: %+v", got)
	}
	if position := s.config(s.connector)["start_page_token"]; position != "p2" {
		t.Errorf("the new position is %v", position)
	}
}

// Drive is down: the document says why, the feed position does not move,
// and the next sync fetches the file.
func TestAFileThatCouldNotBeReachedIsTriedAgain(t *testing.T) {
	s := newSite(t)
	s.google.put(pdf(reportID, "Report.pdf", "version one", "2026-11-01T10:00:00.000Z"))
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")
	if err := s.run(reportID); err != nil {
		t.Fatal(err)
	}
	s.indexedAsReady(report)
	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	changed := pdf(reportID, "Report.pdf", "version two", "2026-11-03T10:00:00.000Z")
	changed.status = 503
	s.google.put(changed)
	if err := s.run(); err == nil {
		t.Fatal("a sync whose download failed reported success")
	}
	got, _ := s.document(report)
	if got.State != "failed" || got.Reason != reasonUnavailable || got.Files != 1 {
		t.Errorf("after a failed download the document is %+v", got)
	}
	if position := s.config(s.connector)["start_page_token"]; position != "p1" {
		t.Errorf("the position moved past a change that was not fetched: %v", position)
	}

	s.google.files[reportID].status = 0
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.document(report); got.State != "pending" || s.stored(got.NewestKey) != "version two" {
		t.Errorf("the retry left the document %+v", got)
	}
	if position := s.config(s.connector)["start_page_token"]; position != "p2" {
		t.Errorf("the position after the retry is %v", position)
	}
}

// Each way a picked file fails says so on its document, in words for the
// person, and stores nothing.
func TestAPickedFileThatCannotBeFetchedSaysWhy(t *testing.T) {
	s := newSite(t)
	big := pdf(reportID, "Big.pdf", strings.Repeat("x", 1<<20+1), "2026-11-01T10:00:00.000Z")
	s.google.put(big)
	s.google.put(pdf(planID, "Empty.pdf", "", "2026-11-01T10:00:00.000Z"))
	s.google.files[planID].body = []byte{}
	tooBig := s.picked(s.owner, s.connector, reportID, "Big.pdf", "application/pdf")
	empty := s.picked(s.owner, s.connector, planID, "Empty.pdf", "application/pdf")
	gone := s.picked(s.owner, s.connector, otherID, "Gone.pdf", "application/pdf")

	if err := s.run(reportID, planID, otherID); err == nil {
		t.Fatal("three failed files reported success")
	}
	for id, reason := range map[int64]string{tooBig: "The file is larger than the 1 MB per-file limit.", empty: reasonEmpty, gone: reasonGone} {
		got, _ := s.document(id)
		if got.State != "failed" || got.Reason != reason || got.Files != 0 {
			t.Errorf("document %d is %+v, want failed with %q", id, got, reason)
		}
	}
	if len(s.indexer.ids) != 0 {
		t.Errorf("failed files were handed to the indexer: %v", s.indexer.ids)
	}

	// The queue is down after the file is stored: the document says so,
	// and keeps its file for a re-index.
	s.google.put(pdf(otherID, "Gone.pdf", "back again", "2026-11-02T10:00:00.000Z"))
	testdatabase.Exec(t, s.pool, `UPDATE documents SET status = '{"state": "pending", "stage": "downloading"}'::jsonb WHERE id = $1`, gone)
	s.indexer.down = true
	if err := s.run(otherID); err == nil {
		t.Fatal("a file whose index job was not queued reported success")
	}
	if got, _ := s.document(gone); got.State != "failed" || got.Reason != reasonQueue || got.Files != 1 {
		t.Errorf("with the queue down the document is %+v", got)
	}
}

// Google refuses the grant: the connection is flagged for a new consent,
// the picked file says why, and the next job does not ask Google again.
func TestARefusedGrantIsFlaggedOnce(t *testing.T) {
	s := newSite(t)
	s.google.dead["refresh-1"] = true
	s.google.put(pdf(reportID, "Report.pdf", "version one", "2026-11-01T10:00:00.000Z"))
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")

	if err := s.run(reportID); err == nil {
		t.Fatal("a job with a refused grant reported success")
	}
	if expired, _ := s.config(s.connector)["auth_expired"].(bool); !expired {
		t.Error("the connection was not flagged for a new consent")
	}
	if got, _ := s.document(report); got.State != "failed" || got.Reason != reasonAuth {
		t.Errorf("the picked file is %+v", got)
	}
	if err := s.run(); err == nil || s.google.refreshes != 1 {
		t.Errorf("a flagged connection asked Google again (%d requests, %v)", s.google.refreshes, err)
	}
	// And the schedule leaves it alone.
	due, err := DueStore{Pool: s.pool}.TakeDue(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range due {
		if connection.ConnectorID == s.connector {
			t.Error("a connection Google refused is still scheduled")
		}
	}
}

// A document waiting for a download whose job never ran is fetched by the
// next sync, once it has waited long enough to be sure.
func TestAnAbandonedDownloadIsPickedUpByTheNextSync(t *testing.T) {
	s := newSite(t)
	s.google.put(pdf(reportID, "Report.pdf", "version one", "2026-11-01T10:00:00.000Z"))
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")

	// Fresh: its job is taken to be on its way.
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.document(report); got.Stage != "downloading" {
		t.Fatalf("a download a moment old was taken for abandoned: %+v", got)
	}
	testdatabase.Exec(t, s.pool, `UPDATE documents SET updated_at = now() - interval '16 minutes' WHERE id = $1`, report)
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.document(report); got.State != "pending" || got.Stage != "" || got.Files != 1 {
		t.Errorf("the abandoned download is %+v", got)
	}
}

// The job is keyed by connection AND account. A message that names
// Alice's connection under Bob's account, or Bob's file under Alice's
// connection, touches nothing of the other's.
func TestOneAccountsJobTouchesNothingOfAnothers(t *testing.T) {
	s := newSite(t)
	alice, aliceConnector := s.owner, s.connector
	bob := testdatabase.Account(t, s.pool)
	bobConnector := s.connect(bob, "refresh-bob", nil)

	s.google.put(pdf(reportID, "Report.pdf", "shared bytes", "2026-11-01T10:00:00.000Z"))
	aliceDocument := s.picked(alice, aliceConnector, reportID, "Report.pdf", "application/pdf")
	bobDocument := s.picked(bob, bobConnector, reportID, "Report.pdf", "application/pdf")

	// Alice's connection named under Bob's account: no such connection.
	if err := s.runFor(bob, aliceConnector, reportID); err != nil {
		t.Fatal(err)
	}
	if s.google.refreshes != 0 || len(s.google.requests) != 0 {
		t.Errorf("a job for a connection that is not the account's reached Google (%d renewals, %v)", s.google.refreshes, s.google.requests)
	}
	for _, id := range []int64{aliceDocument, bobDocument} {
		if got, _ := s.document(id); got.Stage != "downloading" || got.Files != 0 {
			t.Errorf("document %d was touched: %+v", id, got)
		}
	}

	// Alice's own job fetches Alice's document and leaves Bob's, though
	// both are the same Drive file.
	if err := s.runFor(alice, aliceConnector, reportID); err != nil {
		t.Fatal(err)
	}
	mine, _ := s.document(aliceDocument)
	theirs, _ := s.document(bobDocument)
	if mine.Files != 1 || !strings.HasPrefix(mine.NewestKey, "documents/"+alice+"/") {
		t.Errorf("Alice's document is %+v", mine)
	}
	if theirs.Stage != "downloading" || theirs.Files != 0 {
		t.Errorf("Alice's job changed Bob's document: %+v", theirs)
	}
	if s.indexer.count(bobDocument) != 0 {
		t.Error("Alice's job queued Bob's document")
	}
	if _, touched := s.config(bobConnector)["start_page_token"]; touched || s.config(bobConnector)["token"] == s.config(aliceConnector)["token"] {
		t.Error("Alice's job wrote to Bob's connection")
	}

	// A sync of everything for Alice marks a removed file's document gone
	// for Alice only.
	s.indexedAsReady(aliceDocument)
	if err := s.runFor(alice, aliceConnector); err != nil {
		t.Fatal(err)
	}
	s.google.remove(reportID)
	if err := s.runFor(alice, aliceConnector); err != nil {
		t.Fatal(err)
	}
	if got, there := s.document(aliceDocument); !there || got.Gone == "" {
		t.Errorf("Alice's document of a removed file is %+v", got)
	}
	if got, there := s.document(bobDocument); !there || got.Gone != "" {
		t.Errorf("Alice's sync marked Bob's document: %+v", got)
	}
}

// The schedule: a due connection is taken once and moved on by its own
// interval; one that is off, or not due, is not taken.
func TestDueConnectionsAreTakenOnce(t *testing.T) {
	s := newSite(t)
	later := s.connect(testdatabase.Account(t, s.pool), "refresh-later", nil)
	testdatabase.Exec(t, s.pool, `UPDATE search_source_connectors SET next_scheduled_at = now() + interval '1 hour' WHERE id = $1`, later)
	off := s.connect(testdatabase.Account(t, s.pool), "refresh-off", nil)
	testdatabase.Exec(t, s.pool, `UPDATE search_source_connectors SET enabled = false WHERE id = $1`, off)

	schedule := DueStore{Pool: s.pool}
	taken := func() map[int64]string {
		due, err := schedule.TakeDue(context.Background(), 50)
		if err != nil {
			t.Fatal(err)
		}
		found := map[int64]string{}
		for _, connection := range due {
			found[connection.ConnectorID] = connection.UserID
		}
		return found
	}
	first := taken()
	if first[s.connector] != s.owner {
		t.Errorf("the due connection was not taken with its owner: %v", first)
	}
	if _, wrong := first[later]; wrong {
		t.Error("a connection not yet due was taken")
	}
	if _, wrong := first[off]; wrong {
		t.Error("a connection that is switched off was taken")
	}
	if again := taken(); again[s.connector] != "" {
		t.Error("a connection was taken twice in one interval")
	}
	var minutes float64
	_ = s.pool.QueryRow(context.Background(), `SELECT extract(epoch FROM next_scheduled_at - now()) / 60 FROM search_source_connectors WHERE id = $1`, s.connector).Scan(&minutes)
	if minutes < 1439 || minutes > 1441 {
		t.Errorf("the connection is next due in %.1f minutes; its interval is a day", minutes)
	}
}
