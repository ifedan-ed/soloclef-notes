package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// studyFilesOver is a reader of study files over a fresh directory, and a
// function that writes a file into it. Every file is written by the test.
// The directory is returned too, for a test that takes a file away again.
func studyFilesOver(t testing.TB, capacity int) (*StudyFiles, func(key, content string) *StoredFile, string) {
	t.Helper()
	directory := t.TempDir()
	store, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	put := func(key, content string) *StoredFile {
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return &StoredFile{Backend: filestore.LocalBackend, Key: key}
	}
	return newStudyFiles(setOf(t, store), capacity), put, directory
}

func TestAStudyFileIsReadOnceAndRemembered(t *testing.T) {
	ctx := context.Background()
	files, put, directory := studyFilesOver(t, 8)
	deck := put("artifacts/1/1/primary/a.json", deckOf(12))
	if cards, err := files.Deck(ctx, deck); err != nil || cards != 12 {
		t.Fatalf("the first read: %d, %v", cards, err)
	}
	// The file is gone from disk; what it said is still known, because a
	// key's file never changes and so what was read from it stays true.
	if err := os.Remove(filepath.Join(directory, "artifacts", "1", "1", "primary", "a.json")); err != nil {
		t.Fatal(err)
	}
	if cards, err := files.Deck(ctx, deck); err != nil || cards != 12 {
		t.Fatalf("the remembered read: %d, %v", cards, err)
	}
}

func TestAQuizsAnswersAreTheCallersToChange(t *testing.T) {
	ctx := context.Background()
	files, put, _ := studyFilesOver(t, 8)
	quiz := put("artifacts/1/2/primary/q.json", quizOf(2, 0, 3))
	first, err := files.Quiz(ctx, quiz)
	if err != nil || !slices.Equal(first, []int{2, 0, 3}) {
		t.Fatalf("%v, %v", first, err)
	}
	first[0] = 1
	again, err := files.Quiz(ctx, quiz)
	if err != nil || !slices.Equal(again, []int{2, 0, 3}) {
		t.Errorf("changing one answer key changed the remembered one: %v, %v", again, err)
	}
}

func TestOneFileReadAsADeckIsNotAnsweredAsAQuiz(t *testing.T) {
	ctx := context.Background()
	files, put, _ := studyFilesOver(t, 8)
	deck := put("artifacts/1/3/primary/d.json", deckOf(5))
	if _, err := files.Deck(ctx, deck); err != nil {
		t.Fatal(err)
	}
	var notAQuiz unreadable
	if _, err := files.Quiz(ctx, deck); !errors.As(err, &notAQuiz) {
		t.Errorf("a deck read as a quiz answered %v", err)
	}
}

func TestWhatFailsIsNotRemembered(t *testing.T) {
	ctx := context.Background()
	files, put, _ := studyFilesOver(t, 8)

	// A file that is not a deck: refused, and refused again only because it
	// still is not one -- so once it is fixed (which in production would be
	// a new key; here the test rewrites the same one), it reads.
	broken := put("artifacts/1/4/primary/b.json", `{"schema_version": 1, "cards": []}`)
	var notADeck unreadable
	if _, err := files.Deck(ctx, broken); !errors.As(err, &notADeck) {
		t.Fatalf("an empty deck answered %v", err)
	}
	put("artifacts/1/4/primary/b.json", deckOf(3))
	if cards, err := files.Deck(ctx, broken); err != nil || cards != 3 {
		t.Errorf("a refusal was remembered: %d, %v", cards, err)
	}

	// A file the store does not have yet.
	missing := &StoredFile{Backend: filestore.LocalBackend, Key: "artifacts/1/5/primary/m.json"}
	if _, err := files.Deck(ctx, missing); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("a missing file answered %v", err)
	}
	put("artifacts/1/5/primary/m.json", deckOf(4))
	if cards, err := files.Deck(ctx, missing); err != nil || cards != 4 {
		t.Errorf("a store failure was remembered: %d, %v", cards, err)
	}
}

func TestARowThatNamesNoFileOrAnotherStoreIsRefused(t *testing.T) {
	ctx := context.Background()
	files, _, _ := studyFilesOver(t, 8)
	if _, err := files.Deck(ctx, nil); !errors.Is(err, errNoStudyFile) {
		t.Errorf("no file answered %v", err)
	}
	elsewhere := &StoredFile{Backend: "s3", Key: "artifacts/1/6/primary/e.json"}
	var notAStudyFile unreadable
	if _, err := files.Deck(ctx, elsewhere); err == nil || errors.As(err, &notAStudyFile) {
		// A file in a store this process cannot read is the server's
		// problem, not the deck's, and must not be reported as a bad deck.
		t.Errorf("a file in another store answered %v", err)
	}
}

func TestTheLeastRecentlyUsedFileIsForgottenFirst(t *testing.T) {
	ctx := context.Background()
	files, put, _ := studyFilesOver(t, 2)
	a := put("artifacts/1/7/primary/a.json", deckOf(1))
	b := put("artifacts/1/7/primary/b.json", deckOf(2))
	c := put("artifacts/1/7/primary/c.json", deckOf(3))
	for _, file := range []*StoredFile{a, b, a, c} {
		if _, err := files.Deck(ctx, file); err != nil {
			t.Fatal(err)
		}
	}
	// a was used after b, so b is the one the third file pushed out.
	remembered := func(file *StoredFile) bool {
		_, found := files.recall(studyFileKey{backend: file.Backend, key: file.Key})
		return found
	}
	if !remembered(a) || remembered(b) || !remembered(c) {
		t.Errorf("remembered a=%v b=%v c=%v", remembered(a), remembered(b), remembered(c))
	}
	if files.recency.Len() != 2 || len(files.byKey) != 2 {
		t.Errorf("%d entries in order, %d by key", files.recency.Len(), len(files.byKey))
	}
}

// BenchmarkStudyFilesRemembered is what the manifest and every study write
// pay for a deck after the first time: compare BenchmarkStudyFileOrdinaryDeck
// and BenchmarkStudyFileLargestDeck, which are what each paid before.
func BenchmarkStudyFilesRemembered(b *testing.B) {
	ctx := context.Background()
	files, put, _ := studyFilesOver(b, studyFilesRemembered)
	deck := put("artifacts/1/8/primary/l.json", largestDeck())
	if _, err := files.Deck(ctx, deck); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if cards, err := files.Deck(ctx, deck); err != nil || cards != 100 {
			b.Fatal(cards, err)
		}
	}
}

// BenchmarkStudyFilesFirstRead is the same deck read off the disk each time:
// the cost of the first request for a deck, file open included.
func BenchmarkStudyFilesFirstRead(b *testing.B) {
	ctx := context.Background()
	files, put, _ := studyFilesOver(b, studyFilesRemembered)
	deck := put("artifacts/1/9/primary/o.json", deckOf(30))
	b.ReportAllocs()
	for b.Loop() {
		files.mutex.Lock()
		clear(files.byKey)
		files.recency.Init()
		files.mutex.Unlock()
		if cards, err := files.Deck(ctx, deck); err != nil || cards != 30 {
			b.Fatal(cards, err)
		}
	}
}
