package e2eseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/studiooutputs"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_E2ESEED_DATABASE"

const secret = "a-seed-secret-for-this-test-only"

// nothingRemoved satisfies the studio list's routes, which this test only
// reads through.
type nothingRemoved struct{}

func (nothingRemoved) Remove(context.Context, int64) error { return nil }

func TestTheSeedRoutesWriteWhatTheProductReads(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Tides'), (20, '`+bob+`', 'His');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES (300, '`+alice+`', 'Source', 'FILE', '', 's');
INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES (10, '`+alice+`', 300);
INSERT INTO notebook_public_links (id, notebook_id, user_id, share_token, expires_at) VALUES (5, 10, '`+alice+`', 'tok-notebook', NULL);`)

	directory := t.TempDir()
	files, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	outbox := &Outbox{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := Routes{
		Pool: pool, Files: files, Secret: secret, Outbox: outbox, Errors: errorenvelope.Writer{}, Logger: logger,
		MintSession: func(_ context.Context, userID string, at time.Time) (string, error) {
			return "token-for-" + userID, nil
		},
		Now: func() time.Time { return now },
	}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	// The product's own studio list, to read the seeds back through.
	studiooutputs.Routes{Pool: pool, Removal: nothingRemoved{}, Errors: errorenvelope.Writer{}, Logger: logger}.Register(api)

	call := func(with, method, path, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if with != "" {
			request.Header.Set(SecretHeader, with)
		}
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	seed := func(method, path, body string) (int, string) {
		t.Helper()
		return call(secret, method, path, body)
	}
	count := func(query string) (n int) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	expect := func(name string, status int, body string, want int, says ...string) {
		t.Helper()
		if status != want {
			t.Errorf("%s: %d %s", name, status, body)
			return
		}
		for _, piece := range says {
			if !strings.Contains(body, piece) {
				t.Errorf("%s: %s lacks %s", name, body, piece)
			}
		}
	}

	// Without the stack's secret, every route refuses and nothing is
	// written or handed out.
	for _, pair := range Public() {
		method, path, _ := strings.Cut(pair, " ")
		path = strings.NewReplacer("{snapshot_id}", "1", "{link_id}", "5").Replace(path)
		for _, with := range []string{"", "wrong", secret + "x", secret[:len(secret)-1]} {
			status, body := call(with, method, path, `{"email":"alice@example.test","notebook_id":10,"kind":"report"}`)
			expect(pair+" with the secret "+with, status, body, http.StatusForbidden, "invalid e2e secret")
			if strings.Contains(body, "token-for-") {
				t.Errorf("%s handed out a token without the secret", pair)
			}
		}
	}
	if count("notebook_outputs") != 0 || count("notifications") != 0 || count("notebook_public_links WHERE expires_at IS NOT NULL") != 0 {
		t.Fatal("a request without the secret wrote something")
	}

	// A token for the seeded account, by address in any case.
	status, body := seed("POST", TokenPath, `{"email":"ALICE@example.test"}`)
	expect("her token", status, body, 200, `"access_token":"token-for-`+alice+`"`, `"token_type":"bearer"`)
	status, body = seed("POST", TokenPath, `{"email":"nobody@example.test"}`)
	expect("a token for no account", status, body, 404, "no account")

	// Inbox rows, hers alone.
	status, body = seed("POST", NotificationsPath, `{"email":"alice@example.test","notifications":[
		{"type":"document_processing","title":"Indexed","minutes_ago":5,"read":true,"metadata":{"document_id":300}},
		{"type":"document_processing","title":"Indexing","running":true}]}`)
	expect("her notifications", status, body, 201, `"ids":[`)
	if count("notifications WHERE user_id = '"+alice+"'") != 2 || count("notifications WHERE user_id = '"+bob+"'") != 0 ||
		count("notifications WHERE title = 'Indexed' AND read AND created_at = '2026-11-12T08:55:00Z' AND metadata ->> 'document_id' = '300'") != 1 ||
		count("notifications WHERE title = 'Indexing' AND NOT read AND metadata ->> 'status' = 'in_progress' AND message = 'E2E notification'") != 1 {
		t.Error("the seeded notifications are not the two rows asked for, under her")
	}

	// A link expired a second ago; one that is not there is said so.
	status, body = seed("POST", "/__e2e__/public-links/notebook/5/expire", "")
	expect("expiring her notebook link", status, body, 200, `"expired":5`)
	if count("notebook_public_links WHERE id = 5 AND expires_at = '2026-11-12T08:59:59Z'") != 1 {
		t.Error("the link's expiry is not a second ago")
	}
	status, body = seed("POST", "/__e2e__/public-links/99/expire", "")
	expect("expiring a snapshot nobody made", status, body, 404, "no such public link")

	// Every kind, ready: the output, its run, its artifact and its files,
	// all hers, and the files are in the store under the product's keys.
	kinds := []string{"audio", "video", "report", "report_html", "report_docx", "slides", "mindmap", "flashcards", "quiz", "data_table", "infographic"}
	for _, kind := range kinds {
		status, body = seed("POST", SeedOutputPath, `{"notebook_id":10,"kind":"`+kind+`"}`)
		expect("a ready "+kind, status, body, 200, `"output_id":`, `"artifact_id":`)
		if strings.Contains(body, `"artifact_id":null`) {
			t.Errorf("a ready %s has no artifact: %s", kind, body)
		}
	}
	if got := count("notebook_outputs WHERE notebook_id = 10 AND user_id = '" + alice + "' AND source_document_ids = '[300]' AND generated_by ->> 'model' = 'e2e-fake-model'"); got != len(kinds) {
		t.Errorf("%d of %d outputs are hers with her notebook's sources", got, len(kinds))
	}
	if count("artifacts WHERE user_id <> '"+alice+"'")+count("documents WHERE document_type = 'ARTIFACT' AND user_id <> '"+alice+"'") != 0 {
		t.Error("a seeded artifact or its document is not the notebook owner's")
	}
	if count("podcast_runs WHERE status = 'ready' AND duration_seconds = 3 AND artifact_id IS NOT NULL") != 1 ||
		count("deliverable_jobs WHERE status = 'ready' AND progress = 100 AND artifact_id IS NOT NULL AND finished_at IS NOT NULL") != len(kinds)-1 ||
		count("deliverable_jobs WHERE kind = 'video'") != 1 {
		t.Error("audio should be one ready podcast run and every other kind a ready job")
	}
	// A Word report and a deck each have a preview beside the file; a mind
	// map has no file at all.
	if count("artifact_files") != 12 || count("artifact_files WHERE role = 'preview' AND mime_type = 'application/pdf'") != 2 ||
		count("artifacts a WHERE a.format = 'mindmap' AND NOT EXISTS (SELECT 1 FROM artifact_files f WHERE f.artifact_id = a.id)") != 1 {
		t.Errorf("%d artifact files; want 12, two of them previews, none for the mind map", count("artifact_files"))
	}
	rows, err := pool.Query(context.Background(), `SELECT f.storage_key, f.size_bytes, f.storage_backend, a.id FROM artifact_files f JOIN artifacts a ON a.id = f.artifact_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key, backend string
		var size, artifactID int64
		if err := rows.Scan(&key, &size, &backend, &artifactID); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(key, "artifacts/"+alice+"/") || backend != files.Name() {
			t.Errorf("a file is stored as %s in %s", key, backend)
		}
		stored, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(key)))
		if err != nil || int64(len(stored)) != size {
			t.Errorf("%s: %d bytes in the store, %d on the row, %v", key, len(stored), size, err)
		}
	}
	rows.Close()

	// The seeded files are what they say they are.
	for name, magic := range map[string]string{
		"report.pdf": "%PDF-", "source.pdf": "%PDF-", "deck.pptx": "PK", "report.docx": "PK", "table.xlsx": "PK", "infographic.png": "\x89PNG",
	} {
		if !bytes.HasPrefix(fixture(name), []byte(magic)) {
			t.Errorf("the fixture %s does not begin as its kind does", name)
		}
	}
	if !bytes.Contains(fixture("overview.mp4")[:64], []byte("ftyp")) {
		t.Error("the video fixture is not an MP4")
	}
	var deck struct {
		Cards []map[string]string `json:"cards"`
	}
	if err := json.Unmarshal(flashcards("t"), &deck); err != nil || len(deck.Cards) != 6 || deck.Cards[0]["hint_text"] == "" {
		t.Errorf("the deck reads %v, %v", deck, err)
	}

	// A report's own text and an interactive report's own page.
	status, body = seed("POST", SeedOutputPath, `{"notebook_id":10,"kind":"report","title":"Tides explained","markdown":"# Tides\n\nThe moon pulls."}`)
	expect("a report with its text", status, body, 200)
	if count("documents WHERE title = 'Tides explained' AND source_markdown = E'# Tides\\n\\nThe moon pulls.' AND status ->> 'state' = 'ready'") != 1 {
		t.Error("the report's document does not hold the Markdown asked for")
	}
	status, body = seed("POST", SeedOutputPath, `{"notebook_id":10,"kind":"report_html","title":"Page","html":"<h1>Mine</h1>"}`)
	expect("an interactive report with its page", status, body, 200)
	if count("artifact_files WHERE original_filename = 'report.html' AND size_bytes = 13") != 1 {
		t.Error("the interactive report's file is not the page asked for")
	}

	// Running and failed: no artifact, and the product's list says so.
	status, body = seed("POST", SeedOutputPath, `{"notebook_id":10,"kind":"slides","status":"running","title":"Still going"}`)
	expect("a running deck", status, body, 200, `"artifact_id":null`)
	status, body = seed("POST", SeedOutputPath, `{"notebook_id":10,"kind":"audio","status":"failed","title":"Broke"}`)
	expect("a failed podcast", status, body, 200, `"artifact_id":null`)
	// The running job's heartbeat is ahead of the clock, so the studio's
	// sweep of silent jobs leaves it running while a test looks at it.
	if count("deliverable_jobs WHERE title = 'Still going' AND status = 'running' AND progress = 40 AND heartbeat_at > now()") != 1 ||
		count("podcast_runs WHERE title = 'Broke' AND status = 'failed' AND error = 'E2E seeded failure' AND artifact_id IS NULL") != 1 {
		t.Error("the running job or the failed podcast is not as seeded")
	}
	listed := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/notebooks/10/studio/outputs", nil)
	api.Adapter().ServeHTTP(listed, request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: alice, Method: principal.MethodSession})))
	expect("the studio list of the seeds", listed.Code, listed.Body.String(), 200,
		`"title":"Still going","status":"running"`, `"title":"Broke","status":"failed"`,
		`"kind":"slides","title":"E2E slides","status":"ready"`, `"artifact_format":"pptx"`,
		`"kind":"audio","title":"E2E audio","status":"ready"`, `"duration_seconds":3`,
		`"options":{"format":"interactive"}`)

	// Refusals name what was wrong, and leave nothing behind.
	before, storedBefore := count("notebook_outputs"), count("artifact_files")
	for _, c := range []struct {
		name, body string
		want       int
		says       string
	}{
		{"an unknown kind", `{"notebook_id":10,"kind":"hologram"}`, 422, "unknown kind"},
		{"an unknown status", `{"notebook_id":10,"kind":"report","status":"paused"}`, 422, "status"},
		{"a notebook that is not there", `{"notebook_id":999,"kind":"report"}`, 404, "notebook not found"},
		{"a body that is not JSON", `kind=report`, 422, "JSON"},
	} {
		status, body = seed("POST", SeedOutputPath, c.body)
		expect(c.name, status, body, c.want, c.says)
	}
	if count("notebook_outputs") != before || count("artifact_files") != storedBefore {
		t.Error("a refused seed left rows behind")
	}

	// Finishing an output the app queued: the job is ready with an
	// artifact of the output's kind. A finished output's podcast, and an
	// output that is not there, are not "queued".
	testpostgres.Exec(t, pool, `
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, phase) VALUES (900, '`+alice+`', 'artifact', 'Asked for', 'q1', 'queued', 'queued');
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, deliverable_job_id) VALUES (901, 10, '`+alice+`', 'quiz', 'Asked for', 900);`)
	status, body = seed("POST", CompleteOutputPath, `{"notebook_id":10,"output_id":901}`)
	expect("finishing a queued quiz", status, body, 200, `"output_id":901`, `"artifact_id":`)
	if count("deliverable_jobs j JOIN artifacts a ON a.id = j.artifact_id WHERE j.id = 900 AND j.status = 'ready' AND j.progress = 100 AND a.format = 'quiz' AND a.user_id = '"+alice+"'") != 1 {
		t.Error("the queued job was not finished with a quiz of hers")
	}
	status, body = seed("POST", CompleteOutputPath, `{"notebook_id":20,"output_id":901}`)
	expect("her output through his notebook", status, body, 404, "no queued output")
	status, body = seed("POST", CompleteOutputPath, `{"notebook_id":10,"output_id":424242}`)
	expect("an output that is not there", status, body, 404, "no queued output")

	// A PDF source: the document, its original where an upload's is, a
	// chunk on page 2, and the notebook reads it.
	status, body = seed("POST", SeedSourcePath, `{"notebook_id":10,"title":"Asthma guideline"}`)
	expect("a PDF source", status, body, 200, `"document_id":`, `"chunk_id":`)
	if count(`documents d JOIN document_files f ON f.document_id = d.id AND f.user_id = d.user_id
		JOIN chunks c ON c.document_id = d.id AND c.user_id = d.user_id
		JOIN notebook_sources s ON s.document_id = d.id AND s.notebook_id = 10
		WHERE d.title = 'Asthma guideline.pdf' AND d.user_id = '`+alice+`' AND d.document_type = 'FILE'
		  AND f.kind = 'ORIGINAL' AND f.mime_type = 'application/pdf' AND f.storage_key LIKE 'documents/`+alice+`/%/original/%.pdf'
		  AND c.page_start = 2 AND c.page_end = 2 AND c.content = 'Asthma guideline: use a spacer with the inhaler.'`) != 1 {
		t.Error("the seeded source is not a document with its original, a page-2 chunk and a place in the notebook")
	}
}

func TestTheOutboxKeepsTheNewestMailToAnAddress(t *testing.T) {
	outbox := &Outbox{}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Secret: secret, Outbox: outbox, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	last := func(to string) (int, string) {
		request := httptest.NewRequest("GET", LastMailPath+"?to="+to, nil)
		request.Header.Set(SecretHeader, secret)
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	if status, _ := last("alice@example.test"); status != http.StatusNotFound {
		t.Errorf("an empty outbox answered %d", status)
	}
	for _, message := range []mailer.Message{
		{To: "alice@example.test", Subject: "First", TextBody: "one"},
		{To: "bob@example.test", Subject: "His", TextBody: "two"},
		{To: "Alice@Example.test", Subject: "Reset your password", TextBody: "https://notes.example.test/reset?token=t", Sender: "Notes <no-reply@example.test>"},
	} {
		if err := outbox.Send(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	status, body := last("alice@example.test")
	if status != http.StatusOK || !strings.Contains(body, `"subject":"Reset your password"`) || !strings.Contains(body, `"text":"https://notes.example.test/reset?token=t"`) {
		t.Errorf("her newest mail answered %d %s", status, body)
	}
	if strings.Contains(body, "His") {
		t.Error("her mail shows his")
	}
	// The outbox is bounded: the oldest go first.
	for range outboxSize {
		_ = outbox.Send(context.Background(), mailer.Message{To: "carol@example.test"})
	}
	if status, _ := last("bob@example.test"); status != http.StatusNotFound {
		t.Errorf("mail older than the outbox's bound is still there (%d)", status)
	}
}

func TestSeedRoutesAreNotMountedHalfConfigured(t *testing.T) {
	// Without a database the check stops at its first question, so the
	// order of its questions is what a unit test can see: an empty set is
	// refused, and with everything else given it is the database it asks
	// for. The secret's length is checked by the database test's set-up
	// passing and by the case below, on the message.
	if err := (Routes{}).Check(); err == nil {
		t.Error("an empty set of routes passed its check")
	}
	whole := Routes{
		Secret: secret, Outbox: &Outbox{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		MintSession: func(context.Context, string, time.Time) (string, error) { return "", errors.New("unused") },
	}
	if err := whole.Check(); err == nil || !strings.Contains(err.Error(), "database") {
		t.Errorf("no database: %v", err)
	}
}

func TestAShortSeedSecretIsRefused(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	files, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	routes := Routes{
		Pool: pool, Files: files, Outbox: &Outbox{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		MintSession: func(context.Context, string, time.Time) (string, error) { return "", nil },
	}
	for _, short := range []string{"", "guessable", strings.Repeat("s", minSecretChars-1)} {
		routes.Secret = short
		if err := routes.Check(); err == nil || !strings.Contains(err.Error(), "secret") {
			t.Errorf("the secret %q: %v", short, err)
		}
	}
	routes.Secret = strings.Repeat("s", minSecretChars)
	if err := routes.Check(); err != nil {
		t.Errorf("a secret of the least length: %v", err)
	}
}
