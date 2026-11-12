package documentdeletion

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// noVectors is a vector store with nothing in it: these documents have no
// chunks, and the test is about files.
type noVectors struct{}

func (noVectors) Delete(context.Context, []int64) error { return nil }

// storedKeys is every file in a local store, by key, sorted.
func storedKeys(t *testing.T, directory string) []string {
	t.Helper()
	var keys []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		key, err := filepath.Rel(directory, path)
		keys = append(keys, filepath.ToSlash(key))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	return keys
}

// A video is a document, an artifact row, two artifact files (the MP4 and
// its poster) and one narration MP3 a slide, which only the artifact's
// metadata names. Deleting the video must leave none of them in the store:
// once the rows are gone nothing names the files, and they would be kept,
// and paid for, for good. It must also leave every other video's files
// where they are, the account's own and another account's.
func TestDeletingAVideoLeavesNoFileInTheStore(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	directory := t.TempDir()
	store, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(store)
	if err != nil {
		t.Fatal(err)
	}

	deleted := []string{
		"artifacts/a/51/video.mp4", "artifacts/a/51/poster.png",
		"artifacts/a/51/narration/slide-1.mp3", "artifacts/a/51/narration/slide-2.mp3",
		"documents/a/51/source.md",
	}
	kept := []string{
		"artifacts/a/52/video.mp4", "artifacts/a/52/narration/slide-1.mp3",
		"artifacts/b/53/video.mp4", "artifacts/b/53/narration/slide-1.mp3",
	}
	for _, key := range append(append([]string{}, deleted...), kept...) {
		if err := store.Put(ctx, key, []byte("bytes"), "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
	}
	// Slide 3 of the deleted video has no narration (a silent slide), and
	// its metadata also carries the things a slide list can hold that are
	// not slides; none of them may stop the delete.
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (51, '`+alice+`', 'Deleted video', 'ARTIFACT', '', 'hash-51'),
 (52, '`+alice+`', 'Kept video', 'ARTIFACT', '', 'hash-52'),
 (53, '`+bob+`', 'His video', 'ARTIFACT', '', 'hash-53'),
 (54, '`+alice+`', 'Flashcards', 'ARTIFACT', '', 'hash-54');
INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes) VALUES
 (51, '`+alice+`', 'local', 'documents/a/51/source.md', 'source.md', 5);
INSERT INTO artifacts (id, document_id, user_id, format, metadata) VALUES
 (61, 51, '`+alice+`', 'video', '{"slides": [
    {"slide_number": 1, "audio_storage_key": "artifacts/a/51/narration/slide-1.mp3", "storage_backend": "local"},
    {"slide_number": 2, "audio_storage_key": "artifacts/a/51/narration/slide-2.mp3", "storage_backend": "local"},
    {"slide_number": 3, "audio_storage_key": "", "storage_backend": "local"},
    {"slide_number": 4}, "not a slide", null]}'),
 (62, 52, '`+alice+`', 'video', '{"slides": [
    {"slide_number": 1, "audio_storage_key": "artifacts/a/52/narration/slide-1.mp3", "storage_backend": "local"}]}'),
 (63, 53, '`+bob+`', 'video', '{"slides": [
    {"slide_number": 1, "audio_storage_key": "artifacts/b/53/narration/slide-1.mp3", "storage_backend": "local"}]}'),
 (64, 54, '`+alice+`', 'flashcards', '{"slides": "not a list"}');
INSERT INTO artifact_files (artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256) VALUES
 (61, 'primary', 'local', 'artifacts/a/51/video.mp4', 'video.mp4', 'video/mp4', 5, 'c1'),
 (61, 'preview', 'local', 'artifacts/a/51/poster.png', 'poster.png', 'image/png', 5, 'c2'),
 (62, 'primary', 'local', 'artifacts/a/52/video.mp4', 'video.mp4', 'video/mp4', 5, 'c3'),
 (63, 'primary', 'local', 'artifacts/b/53/video.mp4', 'video.mp4', 'video/mp4', 5, 'c4');`)

	removal := Removal{Rows: PostgresRows{Pool: pool}, Vectors: noVectors{}, Files: stores}
	// The video, and an artifact whose metadata has no slide list at all.
	for _, document := range []int64{51, 54} {
		if err := removal.Remove(ctx, document); err != nil {
			t.Fatalf("removing document %d: %v", document, err)
		}
	}

	if left := storedKeys(t, directory); strings.Join(left, "\n") != strings.Join(sortedCopy(kept), "\n") {
		t.Errorf("the store holds\n  %s\nand should hold only the other videos' files\n  %s",
			strings.Join(left, "\n  "), strings.Join(sortedCopy(kept), "\n  "))
	}
	var documents, artifacts, files int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM documents), (SELECT count(*) FROM artifacts), (SELECT count(*) FROM artifact_files)`).
		Scan(&documents, &artifacts, &files); err != nil {
		t.Fatal(err)
	}
	if documents != 2 || artifacts != 2 || files != 2 {
		t.Errorf("left %d documents, %d artifacts and %d artifact files; want the two other videos: 2, 2 and 2", documents, artifacts, files)
	}
}

// A slide that names its audio and not the store it is in cannot be removed
// from anywhere in particular. The delete stops there and keeps the
// document, rather than guess a store or delete the only record of the file.
func TestASlideThatNamesNoStoreKeepsTheVideo(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	store, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(store)
	if err != nil {
		t.Fatal(err)
	}
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (71, '`+testpostgres.Alice+`', 'Video', 'ARTIFACT', '', 'hash-71');
INSERT INTO artifacts (id, document_id, user_id, format, metadata) VALUES
 (81, 71, '`+testpostgres.Alice+`', 'video', '{"slides": [{"slide_number": 1, "audio_storage_key": "narration/slide-1.mp3"}]}');`)

	err = Removal{Rows: PostgresRows{Pool: pool}, Vectors: noVectors{}, Files: stores}.Remove(ctx, 71)
	if !errors.Is(err, filestore.ErrBackendNotRead) {
		t.Fatalf("Remove = %v; want the store that is not named to be refused", err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT status ->> 'state' FROM documents WHERE id = 71`).Scan(&state); err != nil || state != "deleting" {
		t.Errorf("the document is %q, %v; want it kept, marked, so the delete can be asked for again", state, err)
	}
}

func sortedCopy(keys []string) []string {
	sorted := append([]string{}, keys...)
	sort.Strings(sorted)
	return sorted
}
