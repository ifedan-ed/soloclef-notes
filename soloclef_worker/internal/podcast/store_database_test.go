package podcast

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

type storeFixture struct {
	pool   *pgxpool.Pool
	store  Store
	files  string
	userID string
	run    Run
}

func newStoreFixture(t *testing.T, status string) storeFixture {
	t.Helper()
	pool := testdatabase.Open(t)
	userID := testdatabase.Account(t, pool)
	directory := t.TempDir()
	local, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	f := storeFixture{pool: pool, store: Store{Pool: pool, Files: local}, files: directory, userID: userID}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO podcast_runs (user_id, title, status, source_content, spec)
		VALUES ($1::uuid, 'Sepsis: an overview', $2::podcast_status, 'source', $3::jsonb) RETURNING id`,
		userID, status, twoSpeakerBrief).Scan(&f.run.ID); err != nil {
		t.Fatal(err)
	}
	f.run, err = f.store.Load(context.Background(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f storeFixture) status(t *testing.T) string {
	t.Helper()
	status, err := f.store.Status(context.Background(), f.run.ID, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func (f storeFixture) storedObjects(t *testing.T) []string {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(f.files, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	return found
}

func (f storeFixture) count(t *testing.T, sql string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), sql, f.userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestATranscriptIsStoredOnlyWhileDrafting(t *testing.T) {
	f := newStoreFixture(t, statusDrafting)
	ctx := context.Background()
	attached, err := f.store.AttachTranscript(ctx, f.run.ID, f.userID, []byte(`{"turns":[{"speaker":0,"text":"Hi."}]}`))
	if err != nil || !attached {
		t.Fatalf("attached=%v err=%v", attached, err)
	}
	if f.status(t) != statusRendering {
		t.Fatalf("status %q", f.status(t))
	}

	cancelled := newStoreFixture(t, "cancelled")
	attached, err = cancelled.store.AttachTranscript(ctx, cancelled.run.ID, cancelled.userID, []byte(`{"turns":[]}`))
	if err != nil || attached {
		t.Fatalf("a cancelled podcast took a transcript: %v, %v", attached, err)
	}
	if cancelled.status(t) != "cancelled" {
		t.Fatalf("the cancel was overwritten: %q", cancelled.status(t))
	}
	// Another account's podcast is no podcast.
	other := testdatabase.Account(t, f.pool)
	if attached, _ := f.store.AttachTranscript(ctx, f.run.ID, other, []byte(`{}`)); attached {
		t.Fatal("a transcript was written to another account's podcast")
	}
}

func TestAnEpisodeIsKeptAsTheArtifactAndANewTakeRenewsIt(t *testing.T) {
	f := newStoreFixture(t, statusRendering)
	ctx := context.Background()
	documentID, delivered, err := f.store.Deliver(ctx, Delivery{Run: f.run, Markdown: "# Sepsis\n\n**Ada:** Hi.\n", Audio: []byte("ID3 first take"), Seconds: 61.4})
	if err != nil || !delivered || documentID == 0 {
		t.Fatalf("documentID=%d delivered=%v err=%v", documentID, delivered, err)
	}
	var (
		artifactID, generation, duration int
		format, state, legacyKind, path  string
	)
	if err := f.pool.QueryRow(ctx, `
		SELECT a.id, a.generation, a.format, d.status->>'state', a.metadata->'legacy'->>'kind',
		       (a.metadata->>'duration_seconds')::int, d.path
		FROM artifacts a JOIN documents d ON d.id = a.document_id
		WHERE a.user_id = $1::uuid`, f.userID).Scan(&artifactID, &generation, &format, &state, &legacyKind, &duration, &path); err != nil {
		t.Fatal(err)
	}
	if generation != 1 || format != "podcast" || state != "pending" || legacyKind != "podcast" || duration != 61 || path != "/documents/Sepsis: an overview.md" {
		t.Fatalf("artifact %d: generation %d, format %q, document %q, legacy %q, %ds, path %q", artifactID, generation, format, state, legacyKind, duration, path)
	}
	var podcastStatus string
	var podcastArtifact, podcastSeconds int
	if err := f.pool.QueryRow(ctx, `SELECT status::text, artifact_id, duration_seconds FROM podcast_runs WHERE id = $1`, f.run.ID).
		Scan(&podcastStatus, &podcastArtifact, &podcastSeconds); err != nil {
		t.Fatal(err)
	}
	if podcastStatus != statusReady || podcastArtifact != artifactID || podcastSeconds != 61 {
		t.Fatalf("the podcast is %q, artifact %d, %ds", podcastStatus, podcastArtifact, podcastSeconds)
	}
	first := f.storedObjects(t)
	if len(first) != 1 {
		t.Fatalf("%d objects stored, want 1", len(first))
	}

	// A new take: the podcast was sent back through the brief and drafted
	// again.
	testdatabase.Exec(t, f.pool, `UPDATE podcast_runs SET status = 'rendering' WHERE id = $1`, f.run.ID)
	f.run, _ = f.store.Load(ctx, f.run.ID)
	again, delivered, err := f.store.Deliver(ctx, Delivery{Run: f.run, Markdown: "# Sepsis\n\n**Ada:** Again.\n", Audio: []byte("ID3 second take"), Seconds: 30})
	if err != nil || !delivered || again != documentID {
		t.Fatalf("the second take: document %d (first %d), delivered %v, %v", again, documentID, delivered, err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT generation FROM artifacts WHERE id = $1`, artifactID).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("generation %d, %v", generation, err)
	}
	if n := f.count(t, `SELECT count(*) FROM artifact_files f JOIN artifacts a ON a.id = f.artifact_id WHERE a.user_id = $1::uuid`); n != 1 {
		t.Fatalf("%d file rows after a second take, want 1", n)
	}
	second := f.storedObjects(t)
	if len(second) != 1 || second[0] == first[0] {
		t.Fatalf("the first take's file was not replaced: %v then %v", first, second)
	}
	data, _ := os.ReadFile(second[0])
	if string(data) != "ID3 second take" {
		t.Fatalf("the stored episode is %q", data)
	}
}

func TestACancelledPodcastKeepsNoEpisode(t *testing.T) {
	f := newStoreFixture(t, "cancelled")
	_, delivered, err := f.store.Deliver(context.Background(), Delivery{Run: f.run, Markdown: "x", Audio: []byte("ID3"), Seconds: 1})
	if err != nil || delivered {
		t.Fatalf("delivered=%v err=%v", delivered, err)
	}
	if n := f.count(t, `SELECT count(*) FROM artifacts WHERE user_id = $1::uuid`); n != 0 {
		t.Fatalf("%d artifacts for a cancelled podcast", n)
	}
	if objects := f.storedObjects(t); len(objects) != 0 {
		t.Fatalf("a file was left in the store: %v", objects)
	}
	if err := f.store.Fail(context.Background(), f.run.ID, f.userID, "late failure"); err != nil {
		t.Fatal(err)
	}
	if f.status(t) != "cancelled" {
		t.Fatalf("a failure was written over the cancel: %q", f.status(t))
	}
}

// The guarantee itself: a cancel that is being written while the episode
// is delivered either lands first, and the episode is not kept, or waits
// for the delivery. It is never overwritten. Here the cancel holds the row
// first; the delivery waits for it and then finds the podcast cancelled.
func TestADeliveryWaitsForACancelInFlightAndThenRespectsIt(t *testing.T) {
	f := newStoreFixture(t, statusRendering)
	ctx := context.Background()
	cancel, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cancel.Exec(ctx, `UPDATE podcast_runs SET status = 'cancelled' WHERE id = $1 AND status = 'rendering'`, f.run.ID); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		delivered bool
		err       error
	}
	done := make(chan outcome, 1)
	go func() {
		_, delivered, err := f.store.Deliver(ctx, Delivery{Run: f.run, Markdown: "x", Audio: []byte("ID3 audio"), Seconds: 1})
		done <- outcome{delivered, err}
	}()
	select {
	case got := <-done:
		t.Fatalf("the delivery did not wait for the cancel holding the row: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
	if err := cancel.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.delivered {
		t.Fatalf("delivered=%v err=%v after the cancel committed", got.delivered, got.err)
	}
	if f.status(t) != "cancelled" {
		t.Fatalf("the cancel was overwritten: %q", f.status(t))
	}
	if objects := f.storedObjects(t); len(objects) != 0 {
		t.Fatalf("a file was left in the store: %v", objects)
	}
}
