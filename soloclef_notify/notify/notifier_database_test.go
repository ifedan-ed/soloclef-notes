package notify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_notify/internal/notifytest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

func notifier(pool *pgxpool.Pool) (*notify.Notifier, *notifytest.Publisher) {
	published := &notifytest.Publisher{}
	return &notify.Notifier{Database: pool, Publisher: published, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, published
}

type row struct {
	ID       int64
	Type     string
	Title    string
	Message  string
	Read     bool
	Metadata map[string]any
}

func rows(t *testing.T, pool *pgxpool.Pool, userID string) []row {
	t.Helper()
	result, err := pool.Query(context.Background(), `
		SELECT id, type, title, message, read, metadata FROM notifications WHERE user_id = $1::uuid ORDER BY id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	var out []row
	for result.Next() {
		var r row
		var metadata []byte
		if err := result.Scan(&r.ID, &r.Type, &r.Title, &r.Message, &r.Read, &metadata); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(metadata, &r.Metadata); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func document(t *testing.T, pool *pgxpool.Pool, userID, title, kind string, synced bool) int64 {
	t.Helper()
	if !synced {
		return notifytest.Int64(t, pool, `
			INSERT INTO documents (user_id, title, document_type, content, content_hash)
			VALUES ($1::uuid, $2, $3::documenttype, '', md5(random()::text)) RETURNING id`, userID, title, kind)
	}
	connector := notifytest.Int64(t, pool, `
		INSERT INTO search_source_connectors (user_id, name, connector_type, config)
		VALUES ($1::uuid, 'Drive', 'GOOGLE_DRIVE_CONNECTOR', '{}') RETURNING id`, userID)
	return notifytest.Int64(t, pool, `
		INSERT INTO documents (user_id, title, document_type, content, content_hash, connector_id)
		VALUES ($1::uuid, $2, $3::documenttype, '', md5(random()::text), $4) RETURNING id`, userID, title, kind, connector)
}

// A finished Studio output is one row, read from the run's own rows and
// naming its notebook, and its push is queued at once.
func TestAFinishedStudioOutputWritesOneRowAndQueuesItsPush(t *testing.T) {
	pool := notifytest.Open(t)
	user := notifytest.Account(t, pool)
	n, published := notifier(pool)
	notebook := notifytest.Int64(t, pool, `INSERT INTO notebooks (user_id, title) VALUES ($1::uuid, 'Pharmacology') RETURNING id`, user)
	job := notifytest.Int64(t, pool, `
		INSERT INTO deliverable_jobs (user_id, kind, title, tool_call_id, status) VALUES ($1::uuid, 'artifact', 'run', 'call-1', 'ready') RETURNING id`, user)
	output := notifytest.Int64(t, pool, `
		INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, deliverable_job_id)
		VALUES ($1, $2::uuid, 'slides', 'Week 3', $3) RETURNING id`, notebook, user, job)

	if err := n.StudioFinished(context.Background(), notify.StudioRun{UserID: user, JobID: job}); err != nil {
		t.Fatal(err)
	}
	got := rows(t, pool, user)
	if len(got) != 1 || got[0].Type != "studio_output" || got[0].Title != "Slide deck ready" || got[0].Read {
		t.Fatalf("rows = %+v", got)
	}
	m := got[0].Metadata
	if m["output_id"] != float64(output) || m["notebook_id"] != float64(notebook) || m["status"] != "completed" || m["retry"] != false {
		t.Errorf("metadata = %v", m)
	}
	pushed := m["push"].(map[string]any)
	if pushed["title"] != "Slide deck ready" || pushed["body"] != "Pharmacology" || pushed["path"] != fmt.Sprintf("/notebook/%d?pane=studio", notebook) {
		t.Errorf("push = %v", pushed)
	}
	messages := published.Messages()
	if len(messages) != 1 || messages[0].Task != notify.DeliverTask || messages[0].Countdown != 0 {
		t.Fatalf("published = %+v", messages)
	}
	if ids := messages[0].Args[0].([]any); len(ids) != 1 || ids[0] != got[0].ID {
		t.Errorf("the push names %v, not row %d", ids, got[0].ID)
	}

	// Another account's run is not found under this account.
	stranger := notifytest.Account(t, pool)
	if err := n.StudioFinished(context.Background(), notify.StudioRun{UserID: stranger, JobID: job}); err == nil {
		t.Error("a run was reported to an account that does not own it")
	}
}

// Failures arriving together become one row that counts them, with one
// push held back a minute; a row the person has read is closed, and the
// next failure starts a new one.
func TestFailuresOfOneBatchCollapseIntoOneRow(t *testing.T) {
	pool := notifytest.Open(t)
	user := notifytest.Account(t, pool)
	n, published := notifier(pool)
	var wait sync.WaitGroup
	for i := 0; i < 10; i++ {
		id := document(t, pool, user, fmt.Sprintf("file-%d.pdf", i), "FILE", false)
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := n.SourceIndexed(context.Background(), notify.SourceOutcome{DocumentID: id, Failed: true, Reason: "unreadable"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	got := rows(t, pool, user)
	if len(got) != 1 || got[0].Title != "10 imports failed" || got[0].Metadata["count"] != float64(10) {
		t.Fatalf("rows = %+v", got)
	}
	if got[0].Metadata["push"].(map[string]any)["title"] != "10 imports failed" || got[0].Type != "document_processing" {
		t.Errorf("row = %+v", got[0])
	}
	if messages := published.Messages(); len(messages) != 1 || messages[0].Countdown != time.Minute {
		t.Errorf("published = %+v", messages)
	}

	notifytest.Exec(t, pool, `UPDATE notifications SET read = true WHERE user_id = $1::uuid`, user)
	late := document(t, pool, user, "late.pdf", "FILE", false)
	if err := n.SourceIndexed(context.Background(), notify.SourceOutcome{DocumentID: late, Failed: true}); err != nil {
		t.Fatal(err)
	}
	got = rows(t, pool, user)
	if len(got) != 2 || got[1].Title != "late.pdf could not be added" || got[1].Metadata["count"] != float64(1) {
		t.Errorf("after reading: %+v", got)
	}
	if len(published.Messages()) != 2 {
		t.Errorf("the new row's push was not queued")
	}
}

// A success is silent unless slow or large, and never for a synced
// document; a note or a Studio artifact is not a source at all.
func TestOnlySourcesWorthTellingAboutNotify(t *testing.T) {
	pool := notifytest.Open(t)
	user := notifytest.Account(t, pool)
	n, published := notifier(pool)
	quick := document(t, pool, user, "quick.pdf", "FILE", false)
	slow := document(t, pool, user, "slow.pdf", "FILE", false)
	big := document(t, pool, user, "big.pdf", "FILE", false)
	synced := document(t, pool, user, "synced.pdf", "GOOGLE_DRIVE_FILE", true)
	note := document(t, pool, user, "a note", "NOTE", false)
	ctx := context.Background()
	for _, outcome := range []notify.SourceOutcome{
		{DocumentID: quick, Took: 5 * time.Second, Bytes: 1 << 20},
		{DocumentID: slow, Took: notify.SlowIndexing},
		{DocumentID: big, Took: time.Second, Bytes: notify.LargeSource},
		{DocumentID: synced, Took: time.Hour},
		{DocumentID: note, Failed: true},
		{DocumentID: 999999999, Failed: true},
	} {
		if err := n.SourceIndexed(ctx, outcome); err != nil {
			t.Fatalf("%+v: %v", outcome, err)
		}
	}
	got := rows(t, pool, user)
	if len(got) != 1 || got[0].Title != "2 sources are ready" || got[0].Metadata["status"] != "completed" {
		t.Fatalf("rows = %+v", got)
	}
	if len(published.Messages()) != 1 {
		t.Errorf("published %d", len(published.Messages()))
	}
	// A synced document that fails is reported.
	if err := n.SourceIndexed(ctx, notify.SourceOutcome{DocumentID: synced, Failed: true}); err != nil {
		t.Fatal(err)
	}
	if got := rows(t, pool, user); len(got) != 2 || got[1].Title != "synced.pdf could not be added" {
		t.Errorf("rows = %+v", got)
	}
}

// A source of exactly one notebook opens that notebook's sources.
func TestASourceOfOneNotebookLinksToIt(t *testing.T) {
	pool := notifytest.Open(t)
	user := notifytest.Account(t, pool)
	n, _ := notifier(pool)
	notebook := notifytest.Int64(t, pool, `INSERT INTO notebooks (user_id, title) VALUES ($1::uuid, 'N') RETURNING id`, user)
	doc := document(t, pool, user, "a.pdf", "FILE", false)
	notifytest.Exec(t, pool, `INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES ($1, $2::uuid, $3)`, notebook, user, doc)
	if err := n.SourceIndexed(context.Background(), notify.SourceOutcome{DocumentID: doc, Failed: true}); err != nil {
		t.Fatal(err)
	}
	got := rows(t, pool, user)
	if path := got[0].Metadata["push"].(map[string]any)["path"]; path != fmt.Sprintf("/notebook/%d?pane=sources", notebook) {
		t.Errorf("path = %v", path)
	}
}

func TestAutomationRunsNotifyUnlessCancelled(t *testing.T) {
	pool := notifytest.Open(t)
	user := notifytest.Account(t, pool)
	n, published := notifier(pool)
	ctx := context.Background()
	if err := n.AutomationFinished(ctx, notify.AutomationRun{UserID: user, AutomationID: 3, RunID: 9, Name: "Digest", Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if err := n.AutomationFinished(ctx, notify.AutomationRun{UserID: user, AutomationID: 3, RunID: 9, Name: "Digest", Status: "needs_attention"}); err != nil {
		t.Fatal(err)
	}
	if err := n.AutomationFinished(ctx, notify.AutomationRun{UserID: user, AutomationID: 3, RunID: 9, Name: "x", Status: "exploded"}); err == nil {
		t.Error("an unknown ending was accepted")
	}
	got := rows(t, pool, user)
	if len(got) != 1 || got[0].Title != "“Digest” needs your attention" || got[0].Metadata["status"] != "failed" {
		t.Fatalf("rows = %+v", got)
	}
	if p := got[0].Metadata["push"].(map[string]any); p["title"] != "Automation needs your attention" || p["path"] != "/automation/3/runs/9" {
		t.Errorf("push = %v", p)
	}
	if m := published.Messages(); len(m) != 1 || m[0].Countdown != 0 {
		t.Errorf("published = %+v", m)
	}
}

// The mail hook writes a row with nothing of the mail in it.
func TestAMailDraftWaitingIsARowWithoutTheMail(t *testing.T) {
	pool := notifytest.Open(t)
	user := notifytest.Account(t, pool)
	n, published := notifier(pool)
	if err := n.MailDraftWaiting(context.Background(), notify.MailDraft{UserID: user, DraftID: "d-1", WebPath: "/dashboard/mail/drafts"}); err != nil {
		t.Fatal(err)
	}
	got := rows(t, pool, user)
	if len(got) != 1 || got[0].Type != "mail_draft" || got[0].Metadata["draft_id"] != "d-1" || got[0].Metadata["link"] != "/dashboard/mail/drafts" {
		t.Fatalf("rows = %+v", got)
	}
	// The push opens the phone's Drafts waiting screen, not the inbox: the
	// draft is sent or discarded there.
	if path := got[0].Metadata["push"].(map[string]any)["path"]; path != "/mail/drafts" {
		t.Errorf("the push opens %v", path)
	}
	if m := published.Messages(); len(m) != 1 || m[0].Countdown != 30*time.Second {
		t.Errorf("published = %+v", m)
	}
}

// The row is written even when the queue is down: the inbox never loses
// what the push could not say.
func TestTheRowStaysWhenThePushCannotBeQueued(t *testing.T) {
	pool := notifytest.Open(t)
	user := notifytest.Account(t, pool)
	n, published := notifier(pool)
	published.Fail = fmt.Errorf("broker down")
	podcast := notifytest.Int64(t, pool, `INSERT INTO podcast_runs (user_id, title, status) VALUES ($1::uuid, 'Ep', 'failed') RETURNING id`, user)
	if err := n.StudioFinished(context.Background(), notify.StudioRun{UserID: user, PodcastID: podcast, Failed: true}); err == nil {
		t.Error("the failed queue was not reported")
	}
	if got := rows(t, pool, user); len(got) != 1 || got[0].Title != "Audio overview could not be made" || got[0].Metadata["retry"] != true {
		t.Errorf("rows = %+v", got)
	}
}
