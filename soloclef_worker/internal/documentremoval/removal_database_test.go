package documentremoval

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// fakeQdrant records the points it was asked to delete, on the route the
// real one serves.
type fakeQdrant struct {
	*httptest.Server
	mu      sync.Mutex
	deleted []int64
	fail    bool
}

func startQdrant() *fakeQdrant {
	q := &fakeQdrant{}
	q.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collections/chunks/points/delete" || r.URL.Query().Get("wait") != "true" {
			http.NotFound(w, r)
			return
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		var body struct{ Points []int64 }
		_ = json.NewDecoder(r.Body).Decode(&body)
		q.deleted = append(q.deleted, body.Points...)
		_, _ = w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	return q
}

func (q *fakeQdrant) points() []int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	sorted := append([]int64(nil), q.deleted...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

type fixture struct {
	pool    *pgxpool.Pool
	removal Removal
	qdrant  *fakeQdrant
	files   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := testdatabase.Open(t)
	directory := t.TempDir()
	local, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(local)
	if err != nil {
		t.Fatal(err)
	}
	qdrant := startQdrant()
	t.Cleanup(qdrant.Close)
	vectors := Qdrant{URL: qdrant.URL, Alias: "chunks", HTTP: &http.Client{}}
	if err := vectors.Check(); err != nil {
		t.Fatal(err)
	}
	return fixture{pool: pool, removal: Removal{Pool: pool, Vectors: vectors, Files: stores}, qdrant: qdrant, files: directory}
}

// document makes a document with two chunks, an uploaded file, and an
// artifact with its own file, and returns its id, its chunk ids and the
// stored paths.
func (f fixture) document(t *testing.T, userID string, id int64) ([]int64, []string) {
	t.Helper()
	ctx := context.Background()
	testdatabase.Exec(t, f.pool, `INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status)
		VALUES ($1, $2::uuid, 'doc', 'FILE', 'text', 'h', '{"state":"ready"}')`, id, userID)
	var chunks []int64
	for i := 0; i < 2; i++ {
		var chunk int64
		if err := f.pool.QueryRow(ctx, `INSERT INTO chunks (document_id, user_id, content) VALUES ($1, $2::uuid, 'c') RETURNING id`, id, userID).Scan(&chunk); err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk)
	}
	var paths []string
	for _, key := range []string{"documents/" + userID + "/original.pdf", "artifacts/" + userID + "/primary.mp3"} {
		key = key + "-" + strconv.FormatInt(id, 10)
		path := filepath.Join(f.files, key)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	testdatabase.Exec(t, f.pool, `INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes)
		VALUES ($1, $2::uuid, 'local', $3, 'original.pdf', 5)`, id, userID, relative(f.files, paths[0]))
	var artifact int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO artifacts (document_id, user_id, format) VALUES ($1, $2::uuid, 'podcast') RETURNING id`, id, userID).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	testdatabase.Exec(t, f.pool, `INSERT INTO artifact_files (artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256)
		VALUES ($1, 'primary', 'local', $2, 'a.mp3', 'audio/mpeg', 5, 'x')`, artifact, relative(f.files, paths[1]))
	return chunks, paths
}

func relative(root, path string) string {
	rel, _ := filepath.Rel(root, path)
	return filepath.ToSlash(rel)
}

func gone(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still stored", path)
		}
	}
}

func TestADocumentIsRemovedWithItsVectorsAndEveryFile(t *testing.T) {
	f := newFixture(t)
	owner := testdatabase.Account(t, f.pool)
	stranger := testdatabase.Account(t, f.pool)
	chunks, paths := f.document(t, owner, 9200001)
	_, theirs := f.document(t, stranger, 9200002)

	// Another account's id removes nothing of theirs.
	if err := f.removal.Remove(context.Background(), owner, 9200002); err != nil {
		t.Fatal(err)
	}
	if err := f.removal.Remove(context.Background(), owner, 9200001); err != nil {
		t.Fatal(err)
	}
	if got := f.qdrant.points(); len(got) != 2 || got[0] != chunks[0] || got[1] != chunks[1] {
		t.Fatalf("the vector store was asked to delete %v, want %v", got, chunks)
	}
	gone(t, paths)
	for _, path := range theirs {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("another account's file went: %v", err)
		}
	}
	var left int
	_ = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM documents WHERE id IN (9200001, 9200002)`).Scan(&left)
	if left != 1 {
		t.Fatalf("%d of the two documents are left, want the stranger's one", left)
	}
	// Removing it again is not an error.
	if err := f.removal.Remove(context.Background(), owner, 9200001); err != nil {
		t.Fatal(err)
	}
}

// A vector store that does not answer stops the removal with the document
// still there and marked, so running it again finishes it.
func TestARemovalThatFailedPartwayIsFinishedByRunningItAgain(t *testing.T) {
	f := newFixture(t)
	owner := testdatabase.Account(t, f.pool)
	_, paths := f.document(t, owner, 9200003)
	f.qdrant.mu.Lock()
	f.qdrant.fail = true
	f.qdrant.mu.Unlock()
	if err := f.removal.Remove(context.Background(), owner, 9200003); err == nil {
		t.Fatal("a removal whose vectors could not be deleted reported success")
	}
	var state string
	_ = f.pool.QueryRow(context.Background(), `SELECT status->>'state' FROM documents WHERE id = 9200003`).Scan(&state)
	if state != "deleting" {
		t.Fatalf("the document is %q, want it left marked", state)
	}
	f.qdrant.mu.Lock()
	f.qdrant.fail = false
	f.qdrant.mu.Unlock()
	if err := f.removal.Remove(context.Background(), owner, 9200003); err != nil {
		t.Fatal(err)
	}
	gone(t, paths)
}

func TestAnAccountIsErasedOnlyOnceItIsLockedOut(t *testing.T) {
	f := newFixture(t)
	owner := testdatabase.Account(t, f.pool)
	_, first := f.document(t, owner, 9200004)
	_, second := f.document(t, owner, 9200005)
	ctx := context.Background()

	if err := f.removal.EraseAccount(ctx, owner); !errors.Is(err, ErrStillActive) {
		t.Fatalf("an active account: got %v, want ErrStillActive", err)
	}
	if len(f.qdrant.points()) != 0 {
		t.Fatal("an active account's vectors were deleted")
	}
	testdatabase.Exec(t, f.pool, `UPDATE "user" SET is_active = false WHERE id = $1::uuid`, owner)
	if err := f.removal.EraseAccount(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if len(f.qdrant.points()) != 4 {
		t.Fatalf("%d vectors deleted, want 4", len(f.qdrant.points()))
	}
	gone(t, append(first, second...))
	var accounts int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM "user" WHERE id = $1::uuid`, owner).Scan(&accounts)
	if accounts != 0 {
		t.Fatal("the account row is still there")
	}
	// Run again for an account already gone: nothing to do.
	if err := f.removal.EraseAccount(ctx, owner); err != nil {
		t.Fatal(err)
	}
}

// video makes a video artifact's document: the MP4 as its artifact file,
// and two narration MP3s that only the artifact's metadata names, beside a
// silent slide and the things a slide list can hold that are not slides.
// It returns every stored path.
func (f fixture) video(t *testing.T, userID string, id int64) []string {
	t.Helper()
	number := strconv.FormatInt(id, 10)
	keys := []string{
		"artifacts/" + userID + "/" + number + "/video.mp4",
		"artifacts/" + userID + "/" + number + "/narration/slide-1.mp3",
		"artifacts/" + userID + "/" + number + "/narration/slide-2.mp3",
	}
	var paths []string
	for _, key := range keys {
		path := filepath.Join(f.files, key)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	metadata, err := json.Marshal(map[string]any{"slides": []any{
		map[string]any{"slide_number": 1, "audio_storage_key": keys[1], "storage_backend": "local"},
		map[string]any{"slide_number": 2, "audio_storage_key": keys[2], "storage_backend": "local"},
		map[string]any{"slide_number": 3, "audio_storage_key": "", "storage_backend": "local"},
		map[string]any{"slide_number": 4}, "not a slide", nil,
	}})
	if err != nil {
		t.Fatal(err)
	}
	testdatabase.Exec(t, f.pool, `INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status)
		VALUES ($1, $2::uuid, 'video', 'ARTIFACT', 'text', 'h', '{"state":"ready"}')`, id, userID)
	var artifact int64
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO artifacts (document_id, user_id, format, metadata)
		VALUES ($1, $2::uuid, 'video', $3::jsonb) RETURNING id`, id, userID, string(metadata)).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	testdatabase.Exec(t, f.pool, `INSERT INTO artifact_files (artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256)
		VALUES ($1, 'primary', 'local', $2, 'video.mp4', 'video/mp4', 5, 'x')`, artifact, keys[0])
	return paths
}

// stored is how many files the store holds.
func (f fixture) stored(t *testing.T) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(f.files, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			count++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// The folder deletion removes documents one at a time, through Remove. A
// video among them takes its narration with it, and leaves the narration of
// every other video, the account's own and another account's, where it is.
func TestARemovedVideoLeavesNoFileInTheStore(t *testing.T) {
	f := newFixture(t)
	owner := testdatabase.Account(t, f.pool)
	stranger := testdatabase.Account(t, f.pool)
	removed := f.video(t, owner, 9200011)
	kept := f.video(t, owner, 9200012)
	theirs := f.video(t, stranger, 9200013)

	if err := f.removal.Remove(context.Background(), owner, 9200011); err != nil {
		t.Fatal(err)
	}
	gone(t, removed)
	for _, path := range append(kept, theirs...) {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("another video's file went: %v", err)
		}
	}
	if left := f.stored(t); left != len(kept)+len(theirs) {
		t.Errorf("the store holds %d files, want the %d of the two other videos", left, len(kept)+len(theirs))
	}
}

// An erased account's videos leave nothing behind either, and the erasure
// takes nothing of another account's.
func TestAnErasedAccountsVideosLeaveNoFileInTheStore(t *testing.T) {
	f := newFixture(t)
	owner := testdatabase.Account(t, f.pool)
	stranger := testdatabase.Account(t, f.pool)
	mine := append(f.video(t, owner, 9200014), f.video(t, owner, 9200015)...)
	theirs := f.video(t, stranger, 9200016)
	testdatabase.Exec(t, f.pool, `UPDATE "user" SET is_active = false WHERE id = $1::uuid`, owner)

	if err := f.removal.EraseAccount(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	gone(t, mine)
	if left := f.stored(t); left != len(theirs) {
		t.Errorf("the store holds %d files, want the other account's %d", left, len(theirs))
	}
}

// A slide that names its audio and not the store it is in stops the
// removal with the document kept, rather than guess a store.
func TestASlideThatNamesNoStoreKeepsTheDocument(t *testing.T) {
	f := newFixture(t)
	owner := testdatabase.Account(t, f.pool)
	testdatabase.Exec(t, f.pool, `INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status)
		VALUES (9200017, $1::uuid, 'video', 'ARTIFACT', 'text', 'h', '{"state":"ready"}')`, owner)
	testdatabase.Exec(t, f.pool, `INSERT INTO artifacts (document_id, user_id, format, metadata)
		VALUES (9200017, $1::uuid, 'video', '{"slides": [{"slide_number": 1, "audio_storage_key": "narration/slide-1.mp3"}]}')`, owner)
	if err := f.removal.Remove(context.Background(), owner, 9200017); !errors.Is(err, filestore.ErrBackendNotRead) {
		t.Fatalf("Remove = %v; want the store that is not named to be refused", err)
	}
	var left int
	_ = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM documents WHERE id = 9200017 AND status->>'state' = 'deleting'`).Scan(&left)
	if left != 1 {
		t.Fatal("the document was not kept, marked, for the removal to be run again")
	}
}
