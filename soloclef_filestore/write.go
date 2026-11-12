package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
)

// Writer is the write half of the store, for the one kind of write the Go
// tiers make: a new object under a key that carries a fresh uuid.
//
// It is a separate interface from Store on purpose. Nearly every caller only
// reads, and a caller handed a Store cannot write by mistake; the few that
// create a blob (an artifact the chat saved, an uploaded original) are handed
// a Writer and it shows in their fields.
type Writer interface {
	// Name is the backend's name, which the row that owns the blob records
	// so that readers know where to look.
	Name() string

	// PutStream stores everything body yields under key, without holding it
	// in memory, and reports what was stored. The key must be new: an object
	// is written once and never replaced, which is what lets a reader stream
	// a key without asking whether it is changing underneath. A key that
	// already names an object is ErrExists.
	//
	// The object appears whole or not at all. A body that fails partway, a
	// cancelled context or a process that dies leaves no object under the
	// key, so a reader can never be served the first half of an upload.
	PutStream(ctx context.Context, key string, body io.Reader, contentType string) (Stored, error)

	// Put is PutStream for bytes already in hand (a small generated file).
	Put(ctx context.Context, key string, data []byte, contentType string) error

	// Remove deletes the object: for a caller undoing its own write after the
	// database write that was to own the blob failed, and for deleting a
	// document's files. A key that names nothing is not an error: the caller
	// wanted it gone, and a delete that is retried must be able to succeed.
	Remove(ctx context.Context, key string) error
}

// Stored is what a streamed write learned about the object while writing it,
// which is exactly what the row that owns the blob records (`size_bytes`,
// `checksum_sha256`). They are computed from the bytes that were stored, in
// the same pass, so the caller neither buffers the upload to measure it nor
// trusts a length the client claimed.
type Stored struct {
	// Size is the number of bytes stored.
	Size int64
	// SHA256 is their digest, in lowercase hex.
	SHA256 string
}

// ErrExists is returned by a write for a key that already names an object.
var ErrExists = errors.New("filestore: an object is already stored under this key")

// Writer returns the backend new objects are written to: the configured
// one. The other backends a deployment keeps open are for reading rows that
// were written before a migration, and are never written to.
func (s *Stores) Writer() (Writer, error) {
	store, err := s.Named(s.configured)
	if err != nil {
		return nil, err
	}
	writer, writes := store.(Writer)
	if !writes {
		return nil, fmt.Errorf("filestore: the %s backend cannot be written to by this process", s.configured)
	}
	return writer, nil
}

// measured counts and digests a body as it is read, so the size and checksum
// of an upload cost no second pass over it.
type measured struct {
	body   io.Reader
	digest hash.Hash
	size   int64
}

func measure(body io.Reader) *measured {
	return &measured{body: body, digest: sha256.New()}
}

func (m *measured) Read(buffer []byte) (int, error) {
	read, err := m.body.Read(buffer)
	if read > 0 {
		// hash.Hash.Write never fails.
		m.digest.Write(buffer[:read])
		m.size += int64(read)
	}
	return read, err
}

func (m *measured) stored() Stored {
	return Stored{Size: m.size, SHA256: hex.EncodeToString(m.digest.Sum(nil))}
}

// Put stores data under key through the same path a stream takes, so there is
// one write per backend to get right.
func (l *Local) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := l.PutStream(ctx, key, bytes.NewReader(data), contentType)
	return err
}

// Put stores data under key through the same path a stream takes.
func (s *S3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.PutStream(ctx, key, bytes.NewReader(data), contentType)
	return err
}
