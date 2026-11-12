package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// storeWith is a local store over a fresh temporary directory holding the
// given objects. Every byte in these tests is generated here.
func storeWith(t testing.TB, objects map[string][]byte) (*Local, string) {
	t.Helper()
	directory := t.TempDir()
	for key, content := range objects {
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, directory
}

// pattern is n bytes in which every position is recognisable, so a range
// that is off by one reads as different bytes rather than as similar ones.
func pattern(n int) []byte {
	content := make([]byte, n)
	for index := range content {
		content[index] = byte(index*7 + index/251)
	}
	return content
}

const deckKey = "artifacts/7/31/primary/0123456789abcdef0123456789abcdef.json"

func TestOpenStreamsTheWholeObject(t *testing.T) {
	content := pattern(300_000)
	store, _ := storeWith(t, map[string][]byte{deckKey: content})

	stream, err := store.Open(context.Background(), deckKey)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, content) {
		t.Errorf("read %d bytes (%v), want the %d that were stored", len(got), err, len(content))
	}
}

func TestStatDescribesAnObjectWithoutReadingIt(t *testing.T) {
	store, directory := storeWith(t, map[string][]byte{deckKey: pattern(1234)})
	written := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(directory, filepath.FromSlash(deckKey)), written, written); err != nil {
		t.Fatal(err)
	}

	info, err := store.Stat(context.Background(), deckKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 1234 || !info.Modified.Equal(written) {
		t.Errorf("size %d modified %s", info.Size, info.Modified)
	}
	// A file on disk has no media type and none is invented for it.
	if info.ContentType != "" {
		t.Errorf("the local store claimed a content type: %q", info.ContentType)
	}
}

func TestOpenRangeStreamsExactlyTheBytesAsked(t *testing.T) {
	content := pattern(10_000)
	store, _ := storeWith(t, map[string][]byte{deckKey: content})

	for name, test := range map[string]struct {
		offset, length int64
		refused        bool
	}{
		"the first byte":                  {offset: 0, length: 1},
		"a span in the middle":            {offset: 4096, length: 1000},
		"the last byte":                   {offset: 9999, length: 1},
		"the whole object":                {offset: 0, length: 10_000},
		"nothing, at the start":           {offset: 0, length: 0},
		"nothing, at the very end":        {offset: 10_000, length: 0},
		"one byte past the end":           {offset: 9999, length: 2, refused: true},
		"starting past the end":           {offset: 10_001, length: 0, refused: true},
		"a negative offset":               {offset: -1, length: 10, refused: true},
		"a negative length":               {offset: 0, length: -1, refused: true},
		"a length that overflows a sum":   {offset: 5, length: 1<<63 - 1, refused: true},
		"an offset at the largest number": {offset: 1<<63 - 1, length: 1, refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			stream, err := store.OpenRange(context.Background(), deckKey, test.offset, test.length)
			if test.refused {
				if !errors.Is(err, ErrRangeNotSatisfiable) {
					t.Fatalf("got %v, want the range refused", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			got, err := io.ReadAll(stream)
			want := content[test.offset : test.offset+test.length]
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("read %d bytes (%v), want %d", len(got), err, len(want))
			}
		})
	}
}

// The range reader is used two ways: read into a buffer, and (by io.Copy)
// asked to write itself somewhere. Both must hand over the same bytes.
func TestARangeCopiesTheSameBytesItReads(t *testing.T) {
	content := pattern(200_000)
	store, _ := storeWith(t, map[string][]byte{deckKey: content})

	stream, err := store.OpenRange(context.Background(), deckKey, 70_000, 100_001)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var copied bytes.Buffer
	written, err := io.Copy(&copied, stream)
	if err != nil || written != 100_001 || !bytes.Equal(copied.Bytes(), content[70_000:170_001]) {
		t.Errorf("copied %d bytes (%v)", written, err)
	}
}

// An object that shrinks while it is being read must not look like a
// complete read: the response promised a length.
func TestAnObjectThatShrinksUnderARangeIsAnError(t *testing.T) {
	store, directory := storeWith(t, map[string][]byte{deckKey: pattern(5000)})
	path := filepath.Join(directory, filepath.FromSlash(deckKey))

	for name, drain := range map[string]func(io.Reader) error{
		"read into a buffer": func(stream io.Reader) error {
			// Hidden behind a plain Reader so that WriteTo is not used.
			_, err := io.ReadAll(struct{ io.Reader }{stream})
			return err
		},
		"copied with io.Copy": func(stream io.Reader) error {
			_, err := io.Copy(io.Discard, stream)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, pattern(5000), 0o644); err != nil {
				t.Fatal(err)
			}
			stream, err := store.OpenRange(context.Background(), deckKey, 1000, 4000)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if err := os.Truncate(path, 2000); err != nil {
				t.Fatal(err)
			}
			if err := drain(stream); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("got %v, want the short read reported", err)
			}
		})
	}
}

// A key is data out of a row. Whatever it says, it may name only something
// inside the store.
func TestAKeyThatIsNotAKeyIsRefused(t *testing.T) {
	store, directory := storeWith(t, map[string][]byte{deckKey: pattern(10)})
	// Something real just outside the store, for the climbing keys to aim at.
	outside := filepath.Join(filepath.Dir(directory), "outside.txt")
	if err := os.WriteFile(outside, []byte("not an object"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	for name, key := range map[string]string{
		"empty":                       "",
		"the store itself":            ".",
		"absolute":                    outside,
		"climbing out":                "../outside.txt",
		"climbing out from inside":    "artifacts/../../outside.txt",
		"climbing but staying inside": "artifacts/7/../7/31/primary/0123456789abcdef0123456789abcdef.json",
		"a doubled slash":             "artifacts//7",
		"a trailing slash":            "artifacts/7/",
		"a dot element":               "artifacts/./7",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Stat(context.Background(), key); !errors.Is(err, ErrInvalidKey) {
				t.Errorf("Stat: %v", err)
			}
			if stream, err := store.Open(context.Background(), key); !errors.Is(err, ErrInvalidKey) {
				if stream != nil {
					stream.Close()
				}
				t.Errorf("Open: %v", err)
			}
			if stream, err := store.OpenRange(context.Background(), key, 0, 1); !errors.Is(err, ErrInvalidKey) {
				if stream != nil {
					stream.Close()
				}
				t.Errorf("OpenRange: %v", err)
			}
		})
	}
}

// The traversal a string check cannot see: a perfectly ordinary key whose
// path goes through a symbolic link that points out of the store.
func TestASymlinkOutOfTheStoreIsNotFollowed(t *testing.T) {
	store, directory := storeWith(t, map[string][]byte{deckKey: pattern(10)})
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "secret.txt"), []byte("not an object"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(directory, "linked")); err != nil {
		t.Skipf("this filesystem has no symbolic links: %v", err)
	}

	if _, err := store.Stat(context.Background(), "linked/secret.txt"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Stat followed the link: %v", err)
	}
	stream, err := store.Open(context.Background(), "linked/secret.txt")
	if !errors.Is(err, ErrInvalidKey) {
		if stream != nil {
			stream.Close()
		}
		t.Errorf("Open followed the link: %v", err)
	}
}

func TestWhatIsNotStoredIsNotFound(t *testing.T) {
	store, _ := storeWith(t, map[string][]byte{deckKey: pattern(10)})

	for name, key := range map[string]string{
		"no such object":          "artifacts/7/31/primary/ffffffffffffffffffffffffffffffff.json",
		"no such directory":       "artifacts/999/1/primary/x.json",
		"a directory, not a file": "artifacts/7/31",
		"a path through a file":   deckKey + "/more",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Stat(context.Background(), key); !errors.Is(err, ErrNotFound) {
				t.Errorf("Stat: %v", err)
			}
			if stream, err := store.Open(context.Background(), key); !errors.Is(err, ErrNotFound) {
				if stream != nil {
					stream.Close()
				}
				t.Errorf("Open: %v", err)
			}
		})
	}
}

// A store that cannot be read is an outage, and must not be reported as a
// file that happens to be missing.
func TestAnUnreadableObjectIsNotReportedAsMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file; permissions cannot be tested as root")
	}
	store, directory := storeWith(t, map[string][]byte{deckKey: pattern(10)})
	if err := os.Chmod(filepath.Join(directory, filepath.FromSlash(deckKey)), 0); err != nil {
		t.Fatal(err)
	}
	stream, err := store.Open(context.Background(), deckKey)
	if err == nil {
		stream.Close()
		t.Fatal("the object was opened")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidKey) {
		t.Errorf("a permission failure was reported as %v", err)
	}
}

func TestARequestThatWasCancelledOpensNothing(t *testing.T) {
	store, _ := storeWith(t, map[string][]byte{deckKey: pattern(10)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Stat(ctx, deckKey); !errors.Is(err, context.Canceled) {
		t.Errorf("Stat: %v", err)
	}
	if stream, err := store.Open(ctx, deckKey); !errors.Is(err, context.Canceled) {
		if stream != nil {
			stream.Close()
		}
		t.Errorf("Open: %v", err)
	}
}

// A restore moves the old tree aside and puts the restored one at the same
// path. The store is the path, not whichever directory was there when the
// process started: the next read must see the restored tree. (It used to
// hold one directory handle for its whole life, and answered "not found" for
// every newer file until it was restarted.)
func TestAStoreDirectoryReplacedWhileRunningIsSeen(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "object_store")
	write := func(directory, key string, content []byte) {
		t.Helper()
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const newer = "artifacts/7/32/primary/fedcba9876543210fedcba9876543210.json"
	write(root, deckKey, []byte("before the restore"))
	store, err := OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// A stream opened before the swap keeps the file it opened.
	held, err := store.Open(ctx, deckKey)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if err := os.Rename(root, filepath.Join(parent, "object_store.old")); err != nil {
		t.Fatal(err)
	}
	// Between the two halves of the swap there is no store at the path.
	// That is an outage, and must not be reported as a missing file.
	if _, err := store.Stat(ctx, deckKey); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("with the store directory gone: got %v, want the store's own failure", err)
	}
	write(root, deckKey, []byte("after the restore, and longer"))
	write(root, newer, []byte("{}"))

	if info, err := store.Stat(ctx, newer); err != nil || info.Size != 2 {
		t.Errorf("a file only the restored tree has: %+v, %v", info, err)
	}
	stream, err := store.Open(ctx, deckKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(stream)
	stream.Close()
	if string(got) != "after the restore, and longer" {
		t.Errorf("read %q, want the restored tree's bytes", got)
	}
	ranged, err := store.OpenRange(ctx, deckKey, 6, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(ranged)
	ranged.Close()
	if string(got) != "the" {
		t.Errorf("range read %q from the restored tree", got)
	}

	if before, err := io.ReadAll(held); err != nil || string(before) != "before the restore" {
		t.Errorf("the stream opened before the swap read %q (%v)", before, err)
	}
}

func TestOpenNamesTheSettingThatIsWrong(t *testing.T) {
	directory := t.TempDir()
	for name, test := range map[string]struct {
		settings Settings
		opens    bool
	}{
		"the local backend and its directory": {Settings{Backend: "local", LocalRoot: directory}, true},
		"no backend named":                    {Settings{LocalRoot: directory}, false},
		"local with no directory":             {Settings{Backend: "local"}, false},
		"a directory that is not there":       {Settings{Backend: "local", LocalRoot: filepath.Join(directory, "absent")}, false},
		"a backend that does not exist":       {Settings{Backend: "gcs", LocalRoot: directory}, false},
		// A usable directory beside it does not rescue an S3 backend that
		// was given no bucket (s3_test.go has the rest of that backend).
		"s3 with none of its settings": {Settings{Backend: "s3", LocalRoot: directory}, false},
		// The Python setting is lower-cased on the way in; this one is not
		// second-guessed. One spelling, written once.
		"the name in another case": {Settings{Backend: "Local", LocalRoot: directory}, false},
	} {
		t.Run(name, func(t *testing.T) {
			store, err := Open(test.settings)
			if store != nil {
				defer store.Close()
			}
			if (err == nil) != test.opens {
				t.Fatalf("got %v", err)
			}
			if test.opens && store.Name() != LocalBackend {
				t.Errorf("the store is named %q", store.Name())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Benchmarks. `go test . -bench . -benchmem`
//
// The two numbers that matter are MB/s and, more than that, B/op: memory per
// stream must not grow with the object.

const benchmarkObject = 256 << 20 // 256 MiB

func benchmarkStore(b *testing.B) *Local {
	b.Helper()
	directory := b.TempDir()
	path := filepath.Join(directory, "big.bin")
	file, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	// Written in pieces so that the benchmark's own setup does not hold the
	// object in memory either.
	piece := pattern(1 << 20)
	for written := 0; written < benchmarkObject; written += len(piece) {
		if _, err := file.Write(piece); err != nil {
			b.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}
	store, err := OpenLocal(directory)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })
	return store
}

// BenchmarkStreamWholeObject reads a 256 MiB object end to end through a
// consumer that does real work on every byte (a SHA-256), so the figure is
// not flattered by a destination that discards.
func BenchmarkStreamWholeObject(b *testing.B) {
	store := benchmarkStore(b)
	b.SetBytes(benchmarkObject)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		stream, err := store.Open(context.Background(), "big.bin")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(sha256.New(), stream); err != nil {
			b.Fatal(err)
		}
		stream.Close()
	}
}

// BenchmarkStreamWholeObjectDiscarded is the store's own cost with the
// consumer taken out: open, read every byte, close.
func BenchmarkStreamWholeObjectDiscarded(b *testing.B) {
	store := benchmarkStore(b)
	b.SetBytes(benchmarkObject)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		stream, err := store.Open(context.Background(), "big.bin")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, stream); err != nil {
			b.Fatal(err)
		}
		stream.Close()
	}
}

// BenchmarkRangeRead is a player seeking: one megabyte from deep inside the
// object. Its cost must be that of one megabyte, not of the 200 before it.
func BenchmarkRangeRead(b *testing.B) {
	store := benchmarkStore(b)
	const length = 1 << 20
	b.SetBytes(length)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		stream, err := store.OpenRange(context.Background(), "big.bin", 200<<20, length)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, stream); err != nil {
			b.Fatal(err)
		}
		stream.Close()
	}
}

// BenchmarkStat is the cost of the question every ranged request asks first.
func BenchmarkStat(b *testing.B) {
	store := benchmarkStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := store.Stat(context.Background(), "big.bin"); err != nil {
			b.Fatal(err)
		}
	}
}
