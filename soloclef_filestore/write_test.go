package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLocalPutWritesOnceUnderTheRootAndCanBeReadBack(t *testing.T) {
	directory := t.TempDir()
	local, err := OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "artifacts/7/42/primary/0f3c.json"
	if err := local.Put(ctx, key, []byte(`{"a":1}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	body, err := local.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	read, _ := io.ReadAll(body)
	body.Close()
	if string(read) != `{"a":1}` {
		t.Fatalf("read back %q", read)
	}
	// An object is never replaced.
	if err := local.Put(ctx, key, []byte(`other`), ""); !errors.Is(err, ErrExists) {
		t.Fatalf("a second write of the key: %v", err)
	}
	if on, _ := os.ReadFile(filepath.Join(directory, filepath.FromSlash(key))); string(on) != `{"a":1}` {
		t.Fatalf("the first object was changed to %q", on)
	}
	// A second key in the same folders, which already exist.
	if err := local.Put(ctx, "artifacts/7/42/primary/1111.json", []byte(`x`), ""); err != nil {
		t.Fatal(err)
	}
	if err := local.Remove(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a removed object: %v", err)
	}
	if err := local.Remove(ctx, key); err != nil {
		t.Fatalf("removing what is already gone: %v", err)
	}
}

func TestLocalPutCannotLeaveTheStore(t *testing.T) {
	outside := t.TempDir()
	directory := filepath.Join(outside, "store")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	local, err := OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../escaped.txt", "/etc/escaped.txt", "a/../../escaped.txt", ""} {
		if err := local.Put(context.Background(), key, []byte("x"), ""); err == nil {
			t.Errorf("key %q was written", key)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped.txt")); err == nil {
		t.Fatal("a file was written outside the store")
	}
}

func TestStoresWriterIsTheConfiguredBackend(t *testing.T) {
	local, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stores, err := NewStores(local)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := stores.Writer()
	if err != nil || writer.Name() != LocalBackend {
		t.Fatalf("writer=%v err=%v", writer, err)
	}
}

// failingAfter yields its bytes and then fails, as a request body does when
// the client goes away mid-upload.
type failingAfter struct {
	data []byte
	err  error
}

func (f *failingAfter) Read(buffer []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}
	n := copy(buffer, f.data)
	f.data = f.data[n:]
	return n, nil
}

func TestLocalPutStreamReportsTheSizeAndDigestOfWhatItStored(t *testing.T) {
	local, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Larger than io.Copy's buffer, so the count spans several reads.
	content := bytes.Repeat([]byte("soloclef "), 20_000)
	stored, err := local.PutStream(context.Background(), "documents/1/2/original/a.txt", bytes.NewReader(content), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)
	if stored.Size != int64(len(content)) || stored.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("stored = %+v, want %d bytes and %x", stored, len(content), want)
	}
	body, err := local.Open(context.Background(), "documents/1/2/original/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if read, _ := io.ReadAll(body); !bytes.Equal(read, content) {
		t.Fatal("the object read back is not what was streamed in")
	}
}

func TestLocalPutStreamLeavesNothingWhenTheBodyFails(t *testing.T) {
	directory := t.TempDir()
	local, err := OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	gone := errors.New("the client went away")
	_, err = local.PutStream(context.Background(), "documents/1/2/original/half.pdf",
		&failingAfter{data: []byte("%PDF-1.7 half of a file"), err: gone}, "")
	if !errors.Is(err, gone) {
		t.Fatalf("err = %v, want the body's own failure", err)
	}
	if _, err := local.Stat(context.Background(), "documents/1/2/original/half.pdf"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("half an upload is readable as the object: %v", err)
	}
	// Not even the part file: a failed upload must not fill the volume.
	entries, err := os.ReadDir(filepath.Join(directory, "documents", "1", "2", "original"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a failed upload left %d file(s) behind, first %q", len(entries), entries[0].Name())
	}
}

func TestLocalPutStreamStopsWhenItsContextEnds(t *testing.T) {
	local, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := local.PutStream(ctx, "a/b.txt", bytes.NewReader([]byte("x")), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalPutStreamNeverReplacesAnObjectAndLeavesNoPartFile(t *testing.T) {
	directory := t.TempDir()
	local, err := OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A key with no folder is the edge of the folder walk.
	if _, err := local.PutStream(ctx, "top.txt", bytes.NewReader([]byte("first")), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := local.PutStream(ctx, "top.txt", bytes.NewReader([]byte("second")), ""); !errors.Is(err, ErrExists) {
		t.Fatalf("a second write of the key: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "top.txt" {
		t.Fatalf("the store holds %v, want only the object", entries)
	}
	if on, _ := os.ReadFile(filepath.Join(directory, "top.txt")); string(on) != "first" {
		t.Fatalf("the first object was changed to %q", on)
	}
}

func TestLocalPutStreamDoesNotFollowALinkOutOfTheStore(t *testing.T) {
	outside := t.TempDir()
	directory := t.TempDir()
	// A folder inside the store that is really a link to somewhere else.
	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Skipf("this filesystem has no symbolic links: %v", err)
	}
	local, err := OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.PutStream(context.Background(), "escape/planted.txt", bytes.NewReader([]byte("x")), ""); err == nil {
		t.Fatal("a write through a link that leaves the store succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a file was written outside the store: %q", entries[0].Name())
	}
}

// BenchmarkLocalPutStream is what a stored megabyte costs on local disk,
// fsync included: `go test . -run '^$' -bench PutStream -benchmem`.
func BenchmarkLocalPutStream(b *testing.B) {
	local, err := OpenLocal(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	content := bytes.Repeat([]byte{0x5a}, 1<<20)
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		if _, err := local.PutStream(context.Background(), "bench/"+strconv.Itoa(i)+".bin", bytes.NewReader(content), ""); err != nil {
			b.Fatal(err)
		}
	}
}
