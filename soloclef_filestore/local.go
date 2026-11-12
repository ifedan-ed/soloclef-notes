package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Local is the "local" backend: the object for a key is the file at
// root/key.
//
// # Why os.Root
//
// A key comes out of a database row, and a row is data: one bad write, one
// hand-edited value, and "artifacts/../../etc/passwd" is a key. The Python
// backend defends against that by resolving the joined path and checking it
// is still under the root. os.Root does the same job in the kernel's terms --
// every lookup is made relative to a directory handle, and a path that would
// leave that directory is refused, **including through a symbolic link**,
// with no window between the check and the open. So a key can name only what
// is inside the store, whatever the key says.
//
// # Why the root is opened for every read, not once
//
// This type used to open the directory once, at start-up, and keep the
// handle. A handle names a directory, not a path: when a restore moved the
// old tree aside and put the restored one at the same path, the process went
// on reading the detached old tree and answered "not found" for every file
// written since, until it was restarted. The Python backend resolves the
// path on each call and never had the problem.
//
// So only the path is kept, and each read opens the root afresh, uses it,
// and closes it. The escape protection is unchanged -- it is the same
// os.Root, living for one call instead of for the process -- and a stream
// that is already open keeps reading the file it opened, which is what an
// open file means. The price is one openat and one close of a directory per
// call, plus three small allocations. Measured: BenchmarkStat went from
// about 1.2 to about 4.1 microseconds, and a one-megabyte range read
// (BenchmarkRangeRead) from about 75 to about 90 on a noisy host -- a few
// microseconds on a request that takes milliseconds, and nothing per byte:
// whole-object throughput is unchanged. Comparing the kept handle with the
// path on every call (two stats and a compare) would cost about the same
// and add a branch that only a restore ever takes.
type Local struct {
	directory string
}

// OpenLocal checks that the directory the objects are under is there. The
// directory must exist: a store whose volume was not mounted should stop the
// process at start-up, not answer every request with "not found".
func OpenLocal(directory string) (*Local, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("filestore: opening the local store: %w", err)
	}
	root.Close()
	return &Local{directory: directory}, nil
}

// Name is "local", as rows record it.
func (l *Local) Name() string { return LocalBackend }

// Close has nothing to release: no handle outlives a call.
func (l *Local) Close() error { return nil }

// rooted opens the store's directory as it is at this moment. A directory
// that is not there is an outage of the store and is reported as one -- not
// as ErrNotFound, which would turn a missing volume into a 404 per file.
func (l *Local) rooted() (*os.Root, error) {
	root, err := os.OpenRoot(l.directory)
	if err != nil {
		return nil, fmt.Errorf("filestore: opening the local store: %w", err)
	}
	return root, nil
}

// Stat describes the file under the key.
func (l *Local) Stat(ctx context.Context, key string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	name, err := localName(key)
	if err != nil {
		return Info{}, err
	}
	root, err := l.rooted()
	if err != nil {
		return Info{}, err
	}
	defer root.Close()
	stat, err := root.Stat(name)
	if err != nil {
		return Info{}, readFailure(key, err)
	}
	return infoOf(key, stat)
}

// Open streams the whole file.
//
// What is returned is the *os.File itself, not a wrapper, and that is
// deliberate. This package's part is to not hide the file: whoever sends it
// can see that it is one, and can hand it to a destination's ReadFrom so the
// bytes go by sendfile, with no pass through this process's memory.
//
// A bare io.Copy does not get that, even into a writer that offers ReadFrom:
// io.Copy asks the source for WriteTo first, *os.File has one, and it passes
// the destination a wrapper that hides the file. So the sender has to make
// the hand-over itself (objecthttp.Answer.Send does). Nothing here can
// promise sendfile; it only keeps it possible.
func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	file, _, err := l.open(ctx, key)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// OpenRange streams `length` bytes from `offset`.
func (l *Local) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length < 0 {
		return nil, ErrRangeNotSatisfiable
	}
	file, info, err := l.open(ctx, key)
	if err != nil {
		return nil, err
	}
	// Checked against the file that is actually open, not against an earlier
	// Stat, and written so that it cannot overflow: `offset+length` could.
	if offset > info.Size || length > info.Size-offset {
		file.Close()
		return nil, ErrRangeNotSatisfiable
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("filestore: seeking in %q: %w", key, err)
	}
	return &section{file: file, remaining: length}, nil
}

// open is the one place a key becomes an open file: validated, opened inside
// the root, and confirmed to be a regular file.
func (l *Local) open(ctx context.Context, key string) (*os.File, Info, error) {
	// A local read cannot be interrupted once it starts, so the request's
	// cancellation is honoured where it still can be: before the open.
	if err := ctx.Err(); err != nil {
		return nil, Info{}, err
	}
	name, err := localName(key)
	if err != nil {
		return nil, Info{}, err
	}
	root, err := l.rooted()
	if err != nil {
		return nil, Info{}, err
	}
	// The root is needed only to find the file. Once open, the file is its
	// own handle and outlives the root's.
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, Info{}, readFailure(key, err)
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, Info{}, fmt.Errorf("filestore: describing %q: %w", key, err)
	}
	info, err := infoOf(key, stat)
	if err != nil {
		file.Close()
		return nil, Info{}, err
	}
	return file, info, nil
}

// localName turns a key into a path under the root, refusing anything that
// is not a plain relative path (validKey, in filestore.go).
//
// The Python backend also accepted "a/../b" so long as the result stayed
// inside the root; no writer produces such a key, so here it is refused
// outright rather than normalised -- a key is an identifier, and two
// spellings of one identifier is how a deletion misses the blob it meant.
func localName(key string) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	return filepath.FromSlash(key), nil
}

// infoOf is the description of a file that is an object. A directory is not
// one: "artifacts/1" exists on disk and names no blob, so it is reported as
// not found rather than opened and failed on at the first read.
func infoOf(key string, stat fs.FileInfo) (Info, error) {
	if !stat.Mode().IsRegular() {
		return Info{}, fmt.Errorf("%w: %q is not a file", ErrNotFound, key)
	}
	// ContentType stays empty: see Info.
	return Info{Size: stat.Size(), Modified: stat.ModTime()}, nil
}

// readFailure sorts an open or stat error into the three kinds a caller
// handles. Only "does not exist" is ErrNotFound -- which includes a key that
// runs *through* a file ("a.json/more"), where the kernel says "not a
// directory" rather than "no such file" about the same fact. A permission
// error or an I/O error is returned as itself, because answering "not found"
// for a store that cannot be read would hide an outage behind a 404.
func readFailure(key string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("%w: %q", ErrNotFound, key)
	case isEscape(err):
		return fmt.Errorf("%w: %q leaves the store", ErrInvalidKey, key)
	default:
		return fmt.Errorf("filestore: reading %q: %w", key, err)
	}
}

// isEscape reports os.Root's refusal of a path that would leave the root,
// which for a key that already passed fs.ValidPath can only mean a symbolic
// link inside the store points out of it. The standard library exports no
// error value for this, only the message, which is why it is matched by text
// here and pinned by a test (TestASymlinkOutOfTheStoreIsNotFollowed).
func isEscape(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr) && pathErr.Err != nil &&
		pathErr.Err.Error() == "path escapes from parent"
}

// section is `remaining` bytes of an open file, from wherever the file's
// offset now is.
type section struct {
	file      *os.File
	remaining int64
}

// Read hands out at most the bytes that are left. Reaching the end of the
// file with bytes still owed means the object shrank after it was opened;
// that is reported, because a response that promised a Content-Length must
// not end short without the server knowing.
func (s *section) Read(buffer []byte) (int, error) {
	if s.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(buffer)) > s.remaining {
		buffer = buffer[:s.remaining]
	}
	read, err := s.file.Read(buffer)
	s.remaining -= int64(read)
	if err == io.EOF && s.remaining > 0 {
		return read, io.ErrUnexpectedEOF
	}
	return read, err
}

// WriteTo is what lets a range read be as cheap as a whole-file read, when
// the destination allows it.
//
// io.Copy asks its source for a WriterTo before it does anything else. This
// one hands the destination an *io.LimitedReader over the *os.File, which is
// the shape net/http's own response writer recognises in its ReadFrom and
// turns into a sendfile of that many bytes. That is all this package
// guarantees: the shape. A destination with no ReadFrom -- any writer
// wrapped by a middleware that does not forward it -- copies the same bytes
// through a 32 KB buffer in user space instead, which is correct and uses
// constant memory, only slower for a large seek-and-play.
func (s *section) WriteTo(destination io.Writer) (int64, error) {
	limited := &io.LimitedReader{R: s.file, N: s.remaining}
	written, err := io.Copy(destination, limited)
	s.remaining = limited.N
	if err == nil && s.remaining > 0 {
		err = io.ErrUnexpectedEOF
	}
	return written, err
}

func (s *section) Close() error { return s.file.Close() }
