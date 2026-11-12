// Package filestore reads and writes the objects in Soloclef's file store.
//
// The Python tier writes every durable blob -- an uploaded original, an
// artifact's file, a video slide's narration -- through one small object-store
// contract (app/file_storage): an opaque key in, bytes out. Each database row
// that owns a blob records the key and the name of the backend that holds it.
// This package is that contract for the Go tiers: it is what lets a route
// stream a file, and the worker read an uploaded original, without asking
// Python for the bytes.
//
// # What a key is
//
// A key is a slash-separated relative path the writer built, for example
//
//	artifacts/{user_id}/{artifact_id}/{role}/{uuid}{.ext}
//	documents/{user_id}/{document_id}/{kind}/{uuid}{.ext}
//
// The first segment after the kind of object is its owner, so that one
// account's objects are one prefix.
//
// Every key carries a fresh uuid, so an object is written once and never
// overwritten: what a key names does not change while it is being read.
//
// # Which backends exist
//
// Two, and they are the two the Python tier writes to
// (app/file_storage/factory.py):
//
//   - "local": a directory tree on a volume the tiers share, where the object
//     for a key is the file at root/key (local.go).
//   - "s3": a bucket on MinIO or any other S3-compatible store, where the
//     object for a key is the object with that key (s3.go).
//
// One setting, FILE_STORAGE_BACKEND, names the backend new files are written
// to. Which backend a file is *read* from is not that setting's business: it
// is the row's, which recorded where its bytes went. So a process opens every
// backend it was given settings for (Stores, in stores.go) and reads each row
// from the one the row names. There is no fallback from either to the other:
// a key is looked for only in the store its row names. The Store interface's
// three reads map one-to-one onto a HEAD, a GET and a ranged GET, which is
// why the S3 backend needs no more than one request per read.
//
// # Writing
//
// A write makes a new object under a new key and nothing else (write.go): it
// streams, it never replaces, and the object appears whole or not at all.
// Reading and writing are separate interfaces, Store and Writer, so a caller
// that was handed only a Store cannot write by mistake. Writes go to the
// configured backend only; the others are open for the rows already in them.
package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"
)

// The names the backends are recorded under in the `storage_backend` column
// of every row that owns a blob. They are the Python tier's own
// (app/file_storage/settings.py): a row is the contract between the tiers.
const (
	LocalBackend = "local"
	S3Backend    = "s3"
)

// The failures a caller is expected to tell apart. Anything else a Store
// returns is an I/O fault of the store itself.
var (
	// ErrNotFound: no object is stored under the key. A row can outlive its
	// blob (a volume restored from an older backup, a blob removed by hand),
	// so a route must be ready to answer this.
	ErrNotFound = errors.New("filestore: no object is stored under this key")
	// ErrInvalidKey: the text is not a key this store could ever have
	// written -- empty, absolute, or one that climbs out of the store. It is
	// kept apart from ErrNotFound because it means a row is corrupt or a
	// caller passed something that never was a key, which is worth logging
	// differently from a blob that has gone missing.
	ErrInvalidKey = errors.New("filestore: not a storage key")
	// ErrRangeNotSatisfiable: the bytes asked for are not all inside the
	// object.
	ErrRangeNotSatisfiable = errors.New("filestore: the byte range is outside the object")
)

// Info is what a store knows about one object without reading it.
type Info struct {
	// Size is the object's length in bytes, as the store holds it now. A
	// route should send this rather than the size its row recorded: the two
	// agree unless something is wrong, and when something is wrong it is
	// this one that matches the bytes about to be streamed.
	Size int64
	// Modified is when the object was last written.
	Modified time.Time
	// ContentType is the media type the store recorded for the object, and
	// empty when it recorded none. The local backend records none -- a file
	// on disk has no type -- and the S3 backend reports whatever the writer
	// sent with the upload, which is nothing for an object brought over by
	// the copy command. So callers take the type from the row that owns the
	// blob (`mime_type`), exactly as the Python tier does, and treat this
	// field as information. Nothing here sniffs or guesses one from the
	// key's extension.
	ContentType string
}

// Store reads objects by key. Implementations are safe for concurrent use.
//
// The three reads are separate on purpose. A caller that only needs the
// length (to answer a HEAD, or to decide a Range) should not open a stream,
// and a caller that wants bytes 500-999 of a two-gigabyte video must not be
// handed the first 500 to throw away.
type Store interface {
	// Name is the backend's name as rows record it. It is what Stores files
	// a store under, and so what decides which rows are read from it.
	Name() string
	// Stat describes the object without opening a stream.
	Stat(ctx context.Context, key string) (Info, error)
	// Open streams the whole object. The caller closes it.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// OpenRange streams exactly `length` bytes starting at `offset`. A range
	// that is not wholly inside the object is ErrRangeNotSatisfiable -- the
	// caller is expected to have clamped it against Stat -- and a stream that
	// ends early because the object shrank underneath it fails with
	// io.ErrUnexpectedEOF rather than ending quietly short.
	OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
	// Close releases whatever the store holds open.
	Close() error
}

// Settings is everything this package needs from the deployment. It is
// declared here rather than read from the environment here, so that each
// process keeps one place it reads settings from.
type Settings struct {
	// Backend names the configured store, the one new files are written to:
	// the same value as the Python tier's FILE_STORAGE_BACKEND, "local" or
	// "s3". It is required and has no default, so nobody discovers
	// afterwards which one a replica was silently using.
	Backend string
	// LocalRoot is the directory the local backend's objects live under: the
	// path, inside this process's own filesystem, of the same volume the
	// Python tier mounts at its FILE_STORAGE_LOCAL_PATH. Required when
	// Backend is "local". When Backend is "s3" it may be left empty, and
	// then rows recorded "local" are refused; given, they are read from it.
	LocalRoot string
	// S3 is where the bucket is. Every field is required when Backend is
	// "s3". When Backend is "local" it is all given -- and rows recorded
	// "s3" are read from it -- or all left empty.
	S3 S3Settings
}

// S3Settings mirrors the Python tier's six FILE_STORAGE_S3_* settings, one
// field each. None has a default: an endpoint, a bucket or a region written
// here would be one deployment's own values shipped to every other.
type S3Settings struct {
	// Endpoint is a scheme and a host, with a port if there is one:
	// "https://objects.example.com" or "http://minio:9000". The scheme is
	// the one place TLS is chosen; there is no separate switch that could
	// disagree with it.
	Endpoint string
	// Bucket is the one bucket every key is read from.
	Bucket string
	// Region is the region requests are signed for. It is required even for
	// MinIO, which accepts the one it was started with: with it given, the
	// client never makes an extra request per bucket to ask for it.
	Region string
	// PathStyle addresses the bucket as endpoint/bucket/key (what MinIO
	// serves out of the box) when true, and as bucket.endpoint/key (what
	// AWS and most hosted stores expect) when false.
	PathStyle bool
	// AccessKey and SecretKey are the credential's two halves, as values:
	// the process's configuration has already read them out of the files the deployment
	// names, so this package never touches the environment or a secret file.
	AccessKey string
	SecretKey string
}

// Open builds the one store settings.Backend names, or says exactly which
// setting is wrong. A process that serves rows wants OpenStores, which calls
// this once per backend it was given. There is one path per backend and no fallback between them: an
// unknown name is an error at start-up, not a quiet switch to local disk,
// and an S3 store that cannot be reached is an error at start-up too, not a
// quiet read from a volume that happens to be mounted.
func Open(settings Settings) (Store, error) {
	switch settings.Backend {
	case LocalBackend:
		if settings.LocalRoot == "" {
			return nil, errors.New("filestore: the local backend needs the directory its objects are under")
		}
		// Not `return OpenLocal(...)`: a nil *Local returned as a Store is a
		// non-nil interface, and the caller's `store != nil` would lie.
		local, err := OpenLocal(settings.LocalRoot)
		if err != nil {
			return nil, err
		}
		return local, nil
	case S3Backend:
		// The same care as above, for the same reason.
		bucket, err := OpenS3(settings.S3)
		if err != nil {
			return nil, err
		}
		return bucket, nil
	case "":
		return nil, errors.New("filestore: no storage backend is named")
	default:
		return nil, fmt.Errorf("filestore: %q is not a storage backend this tier reads (%q and %q exist)",
			settings.Backend, LocalBackend, S3Backend)
	}
}

// validKey is the one rule for what a key may be, shared by both backends.
//
// fs.ValidPath is the whole rule: slash-separated, not empty, not rooted, and
// with no "." or ".." element and no empty element. Every key the writers
// build passes it. On local disk the rule is what keeps a key inside the
// store. A bucket has no directories to climb out of -- "a/../b" is simply
// another object name there -- but the rule is applied to it all the same,
// because a key is an identifier and must mean one thing whichever backend
// holds it: a row that is refused on one store must not be served on the
// other.
func validKey(key string) error {
	if !fs.ValidPath(key) || key == "." {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	return nil
}
