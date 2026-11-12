package artifacts

// A row is read from the backend it recorded, not from the configured one.
//
// These are the tests of the day a deployment moves FILE_STORAGE_BACKEND from
// "local" to "s3": the files written before are rows recorded "local", still
// on the volume, and must still be served -- from the volume, and never by
// looking for their key in the bucket.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// bucket is an object store held in memory and recorded under any backend
// name a test gives it. It notes every key it is asked for, which is how a
// test proves a store was *not* looked in.
type bucket struct {
	name    string
	objects map[string][]byte

	mutex sync.Mutex
	asked []string
}

func (b *bucket) Name() string { return b.name }
func (b *bucket) Close() error { return nil }

func (b *bucket) find(key string) ([]byte, error) {
	b.mutex.Lock()
	b.asked = append(b.asked, key)
	b.mutex.Unlock()
	content, stored := b.objects[key]
	if !stored {
		return nil, filestore.ErrNotFound
	}
	return content, nil
}

func (b *bucket) askedFor() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return slices.Clone(b.asked)
}

func (b *bucket) Stat(_ context.Context, key string) (filestore.Info, error) {
	content, err := b.find(key)
	if err != nil {
		return filestore.Info{}, err
	}
	return filestore.Info{Size: int64(len(content)), Modified: time.Unix(1_700_000_000, 0)}, nil
}

func (b *bucket) Open(_ context.Context, key string) (io.ReadCloser, error) {
	content, err := b.find(key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (b *bucket) OpenRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	content, err := b.find(key)
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 || offset > int64(len(content)) || length > int64(len(content))-offset {
		return nil, filestore.ErrRangeNotSatisfiable
	}
	return io.NopCloser(bytes.NewReader(content[offset : offset+length])), nil
}

// afterTheMove is a studio whose deployment now writes to a bucket and still
// has the volume mounted. One key is in both stores with different bytes:
// the film on the disk (row 88, recorded "local", written before the move)
// and something else entirely in the bucket. Whatever is served under that
// key says which store was read.
func afterTheMove(t *testing.T) (s *studio, onDisk, inBucket []byte, s3 *bucket) {
	t.Helper()
	s, onDisk = videoStudio(t)
	inBucket = bytes.Repeat([]byte("bucket "), 1000)
	s3 = &bucket{name: filestore.S3Backend, objects: map[string][]byte{videoStorageKey: inBucket}}
	s.stores = setOf(t, s3, s.files)
	return s, onDisk, inBucket, s3
}

func TestAFileWrittenBeforeTheMoveToABucketIsStillReadOffTheDisk(t *testing.T) {
	s, onDisk, _, s3 := afterTheMove(t)

	got := s.get(t, fileURL, nil)
	if got.status != 200 || !bytes.Equal(got.raw, onDisk) {
		t.Fatalf("status %d, %d bytes: the row recorded local did not get the disk's bytes", got.status, len(got.raw))
	}
	// Seeking goes to the same store.
	ranged := s.get(t, fileURL, map[string][]string{"Range": {"bytes=1000-1999"}})
	if ranged.status != 206 || !bytes.Equal(ranged.raw, onDisk[1000:2000]) {
		t.Errorf("a range: status %d, %d bytes", ranged.status, len(ranged.raw))
	}
	if asked := s3.askedFor(); len(asked) != 0 {
		t.Errorf("the bucket was looked in for a row recorded local: %v", asked)
	}
	if strings.Contains(s.log.String(), "level=ERROR") {
		t.Errorf("logged an error: %s", s.log)
	}
}

// The same key, the other row: one recorded "s3" gets the bucket's object,
// though the disk holds a file under that very key.
func TestAFileWrittenAfterTheMoveIsReadFromTheBucket(t *testing.T) {
	s, _, inBucket, _ := afterTheMove(t)
	s.file.Backend = filestore.S3Backend

	got := s.get(t, fileURL, nil)
	if got.status != 200 || !bytes.Equal(got.raw, inBucket) {
		t.Fatalf("status %d, %d bytes: the row recorded s3 did not get the bucket's bytes", got.status, len(got.raw))
	}
}

// The directory was not given beside the bucket, so "local" is not read. The
// bucket holds an object under the row's key, and serving that would be
// serving a different file: the row is refused, and the bucket is not asked.
func TestARowRecordedLocalIsRefusedWhenTheDirectoryWasNotGiven(t *testing.T) {
	s, _, _, s3 := afterTheMove(t)
	s.stores = setOf(t, s3)

	got := s.get(t, fileURL, nil)
	if got.status != 500 {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	if !strings.Contains(s.log.String(), `not one this process reads: \"local\" (it reads \"s3\")`) {
		t.Errorf("the log does not say which backend is missing: %s", s.log)
	}
	if asked := s3.askedFor(); len(asked) != 0 {
		t.Errorf("the bucket was looked in for a row recorded local: %v", asked)
	}
}

// A name that is no backend at all (a corrupt row, a backend a later Python
// tier adds first) is refused out loud, with both real stores right there.
func TestARowRecordedInAnUnknownBackendIsRefused(t *testing.T) {
	for _, recorded := range []string{"gcs", "", "LOCAL", "local "} {
		t.Run("recorded as "+recorded, func(t *testing.T) {
			s, _, _, s3 := afterTheMove(t)
			s.file.Backend = recorded

			got := s.get(t, fileURL, nil)
			if got.status != 500 || got.message(t) != "An internal error occurred. Please try again or report this issue if it persists." {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if !strings.Contains(s.log.String(), `level=ERROR msg="an artifact route failed"`) ||
				!strings.Contains(s.log.String(), `(it reads \"local\" and \"s3\")`) {
				t.Errorf("the log does not name the backends that are read: %s", s.log)
			}
			if asked := s3.askedFor(); len(asked) != 0 {
				t.Errorf("the bucket was looked in: %v", asked)
			}
			// The name of a backend is the server's business.
			if strings.Contains(string(got.raw), "backend") {
				t.Errorf("the caller was told about backends: %s", got.raw)
			}
		})
	}
}

// A slide's narration follows the slide's own record, and a slide written
// before the backend was recorded is looked for in the configured backend --
// where the Python tier looks -- and nowhere else.
func TestASlidesNarrationIsReadFromTheBackendTheSlideRecorded(t *testing.T) {
	onDisk := bytesOf(48_000)
	inBucket := bytes.Repeat([]byte("narration in the bucket "), 500)
	for name, test := range map[string]struct {
		recorded string
		want     []byte
		status   int
	}{
		"recorded local, after the move":  {`, "storage_backend": "local"`, onDisk, 200},
		"recorded s3":                     {`, "storage_backend": "s3"`, inBucket, 200},
		"no backend recorded":             {``, inBucket, 200},
		"recorded in no backend there is": {`, "storage_backend": "gcs"`, nil, 500},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStudio(t)
			s.put(t, audioStorageKey, onDisk)
			s3 := &bucket{name: filestore.S3Backend, objects: map[string][]byte{audioStorageKey: inBucket}}
			s.stores = setOf(t, s3, s.files)
			s.slides = &SlideSet{Format: "video", Slides: []byte(
				`[{"slide_number": 2, "audio_storage_key": "` + audioStorageKey + `"` + test.recorded + `}]`)}

			got := s.get(t, slideURL, nil)
			if got.status != test.status {
				t.Fatalf("status %d, want %d: %s", got.status, test.status, got.raw)
			}
			if test.status == 200 && !bytes.Equal(got.raw, test.want) {
				t.Errorf("%d bytes from the wrong store", len(got.raw))
			}
		})
	}
}

// The study cache remembers by backend and key together. The same key is a
// five-card deck on the disk and a nine-card deck in the bucket; each row
// gets its own count, the first time and from memory, and each store is read
// once.
func TestStudyFilesAreRememberedPerBackend(t *testing.T) {
	ctx := context.Background()
	const key = "artifacts/1/1/primary/same-key.json"
	directory := t.TempDir()
	disk, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	local := &counted{Store: disk}
	writeFile(t, directory, key, deckOf(5))
	s3 := &bucket{name: filestore.S3Backend, objects: map[string][]byte{key: []byte(deckOf(9))}}
	files := newStudyFiles(setOf(t, s3, local), 8)

	onDisk := &StoredFile{Backend: filestore.LocalBackend, Key: key}
	inBucket := &StoredFile{Backend: filestore.S3Backend, Key: key}
	// Interleaved, and asked three times over: a cache keyed by key alone
	// would answer the second row with the first row's deck.
	for round := range 3 {
		if cards, err := files.Deck(ctx, onDisk); err != nil || cards != 5 {
			t.Fatalf("round %d, the row recorded local: %d cards, %v", round, cards, err)
		}
		if cards, err := files.Deck(ctx, inBucket); err != nil || cards != 9 {
			t.Fatalf("round %d, the row recorded s3: %d cards, %v", round, cards, err)
		}
	}
	if local.opened != 1 || len(s3.askedFor()) != 1 {
		t.Errorf("read the disk %d times and the bucket %d times, want once each", local.opened, len(s3.askedFor()))
	}
}

// A study file in a backend this process was given no settings for is the
// server's problem. It is not reported as a bad deck, it is not remembered,
// and the stores that are there are not looked in.
func TestAStudyFileInABackendThatIsNotReadIsRefused(t *testing.T) {
	ctx := context.Background()
	const key = "artifacts/1/2/primary/deck.json"
	s3 := &bucket{name: filestore.S3Backend, objects: map[string][]byte{key: []byte(deckOf(4))}}
	files := newStudyFiles(setOf(t, s3), 8)

	for _, recorded := range []string{filestore.LocalBackend, "gcs", ""} {
		_, err := files.Deck(ctx, &StoredFile{Backend: recorded, Key: key})
		var badDeck unreadable
		if !errors.Is(err, filestore.ErrBackendNotRead) || errors.As(err, &badDeck) {
			t.Errorf("a row recorded %q answered %v", recorded, err)
		}
	}
	if asked := s3.askedFor(); len(asked) != 0 {
		t.Errorf("the bucket was looked in: %v", asked)
	}
	if len(files.byKey) != 0 {
		t.Errorf("a refusal was remembered: %d entries", len(files.byKey))
	}
	// The same key, recorded where it is, still reads.
	if cards, err := files.Deck(ctx, &StoredFile{Backend: filestore.S3Backend, Key: key}); err != nil || cards != 4 {
		t.Errorf("the row recorded s3: %d cards, %v", cards, err)
	}
}

// counted is a store that counts the streams it opens.
type counted struct {
	filestore.Store
	opened int
}

func (c *counted) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	c.opened++
	return c.Store.Open(ctx, key)
}

// writeFile puts a file under a key of a local store's directory.
func writeFile(t testing.TB, directory, key, content string) {
	t.Helper()
	path := filepath.Join(directory, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
