//go:build unix

package filestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

// partSuffix marks a file that is still being written. No key ends in it (a
// key ends in the uploaded file's extension or in none), and a reader asks
// for keys, so a part file is never served.
const partSuffix = ".part"

// PutStream writes the body to a part file beside where the object will be,
// then gives it the object's name in one step.
//
// Why not write the object's own file directly: a reader that opened the key
// halfway through would be served half a file, and a worker killed mid-write
// would leave a short file that is served as the object for ever. With a part
// file the object's name does not exist until every byte is on disk.
//
// The last step is a hard link, not a rename. rename(2) replaces whatever is
// at the destination, and an object is written once: link(2) fails if the name
// is taken, so two writers of one key cannot both succeed and the loser learns
// it (ErrExists). Both names are then one file; the part name is removed.
//
// Everything is done under handles opened through os.Root, so a key cannot
// name a path outside the store however it is spelled, and a symbolic link
// planted inside the store cannot carry a write out of it.
func (l *Local) PutStream(ctx context.Context, key string, body io.Reader, _ string) (Stored, error) {
	if err := ctx.Err(); err != nil {
		return Stored{}, err
	}
	if err := validKey(key); err != nil {
		return Stored{}, err
	}
	root, err := l.rooted()
	if err != nil {
		return Stored{}, err
	}
	defer root.Close()

	// os.Root has no MkdirAll in this Go version; walk the key's folders.
	folder, name := path.Split(key)
	folder = strings.TrimSuffix(folder, "/")
	if folder != "" {
		segments := strings.Split(folder, "/")
		for i := 1; i <= len(segments); i++ {
			directory := strings.Join(segments[:i], string(os.PathSeparator))
			if err := root.Mkdir(directory, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return Stored{}, fmt.Errorf("filestore: creating %q: %w", directory, err)
			}
		}
	}
	// The object's folder, as a handle. The link below names both files
	// relative to it, with no slash in either name, so it cannot leave it.
	directory, err := root.Open(localFolder(folder))
	if err != nil {
		return Stored{}, fmt.Errorf("filestore: opening the folder of %q: %w", key, err)
	}
	defer directory.Close()
	inFolder, err := root.OpenRoot(localFolder(folder))
	if err != nil {
		return Stored{}, fmt.Errorf("filestore: opening the folder of %q: %w", key, err)
	}
	defer inFolder.Close()

	// A random part name, so two uploads of the same key (a retried request)
	// do not write into each other's file.
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Stored{}, fmt.Errorf("filestore: naming a part file: %w", err)
	}
	part := "." + name + "." + hex.EncodeToString(random[:]) + partSuffix
	file, err := inFolder.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Stored{}, fmt.Errorf("filestore: creating a part file for %q: %w", key, err)
	}
	// Whatever happens below, the part name goes: on success it is the second
	// name of the finished object, on failure it is litter.
	defer inFolder.Remove(part)

	counted := measure(&cancellable{ctx: ctx, body: body})
	_, writeErr := io.Copy(file, counted)
	if writeErr == nil {
		// The rename-style promise is only as good as the bytes being on disk
		// when the name appears: without this a power cut after the link can
		// leave the object's name on a file of zeros.
		writeErr = file.Sync()
	}
	// Close reports a write the kernel deferred; it is part of the write.
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return Stored{}, fmt.Errorf("filestore: writing %q: %w", key, writeErr)
	}

	descriptor := int(directory.Fd())
	if err := unix.Linkat(descriptor, part, descriptor, name, 0); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Stored{}, fmt.Errorf("%w: %q", ErrExists, key)
		}
		return Stored{}, fmt.Errorf("filestore: naming %q: %w", key, err)
	}
	// The new name is a change to the folder, and it is the folder that has
	// to reach the disk for the object to survive a crash.
	if err := directory.Sync(); err != nil {
		return Stored{}, fmt.Errorf("filestore: recording %q in its folder: %w", key, err)
	}
	return counted.stored(), nil
}

// localFolder is the path os.Root wants for a key's folder: "." for a key
// with none.
func localFolder(folder string) string {
	if folder == "" {
		return "."
	}
	return strings.ReplaceAll(folder, "/", string(os.PathSeparator))
}

// cancellable stops a copy when its context ends. A local write cannot be
// interrupted inside the kernel, but a long upload is many reads, and each
// one is a chance to notice that nobody is waiting for it any more.
type cancellable struct {
	ctx  context.Context
	body io.Reader
}

func (c *cancellable) Read(buffer []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.body.Read(buffer)
}

// Remove deletes the file under the key. A key that names nothing is not an
// error.
func (l *Local) Remove(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := localName(key)
	if err != nil {
		return err
	}
	root, err := l.rooted()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("filestore: removing %q: %w", key, err)
	}
	return nil
}
