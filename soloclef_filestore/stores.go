package filestore

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ErrBackendNotRead: a row names a storage backend this process was given no
// settings for (or a name that is no backend at all). It is the server's
// fault, never the caller's and never the file's: the bytes exist, in a
// place this process was not told how to reach.
var ErrBackendNotRead = errors.New("filestore: the storage backend is not one this process reads")

// Stores is every store this process can read, by the name rows record.
//
// # Why there is more than one
//
// A row records the backend its bytes were written to, and that record is
// what a read follows (the Python tier's app/file_storage/factory.py does
// the same). FILE_STORAGE_BACKEND only says where *new* files go. So the day
// a deployment moves it from "local" to "s3", every file written before is
// still a "local" row, and still on the volume. A process holding only the
// configured store would refuse every one of them.
//
// # What is in the set, and what is not a fallback
//
// The configured backend, always. Beside it, each other backend whose
// settings the deployment gave: "local" when a directory is named, "s3" when
// the bucket is. A backend with no settings is not in the set, and nothing
// is inferred, probed for or defaulted.
//
// A key is only ever looked for in the store its row names. Named answers
// for exactly one name; there is no method that tries a key in one store
// and then another, because the same key in a different store may be a
// different object.
type Stores struct {
	configured string
	byName     map[string]Store
}

// OpenStores opens every store the settings describe, or says which setting
// is wrong.
//
// Every store that is described must open. A second backend that is
// configured but unreachable stops the process at start-up exactly as the
// first would, rather than starting and answering 500 for every older file:
// the deployment said those rows should be readable.
func OpenStores(settings Settings) (*Stores, error) {
	// The configured backend first, so that a wrong or missing name is the
	// error reported, by Open's own wording.
	configured, err := Open(settings)
	if err != nil {
		return nil, err
	}
	stores := &Stores{configured: settings.Backend, byName: map[string]Store{settings.Backend: configured}}

	// Then whichever other backend was given settings. "Given" is the plain
	// test: a directory was named; any field of the bucket was filled in. A
	// bucket filled in by half is opened too, and OpenS3 refuses it by
	// naming what is missing -- half a bucket is a mistake to report, not a
	// reason to leave "s3" quietly out of the set.
	also := map[string]bool{
		LocalBackend: settings.LocalRoot != "",
		S3Backend:    settings.S3 != S3Settings{},
	}
	for _, name := range []string{LocalBackend, S3Backend} {
		if name == settings.Backend || !also[name] {
			continue
		}
		other := settings
		other.Backend = name
		store, err := Open(other)
		if err != nil {
			stores.Close()
			return nil, fmt.Errorf("%w (the %q backend is not the configured one, but its settings are given, so rows recorded in it are to be readable)",
				err, name)
		}
		stores.byName[name] = store
	}
	return stores, nil
}

// NewStores is a set made of stores that are already open: `configured` is
// the backend new files go to, and `alsoRead` the others. It is how a caller
// that built its own stores (a test, a wrapper around one) makes a set. Two
// stores under one name is an error, since a row could then mean either.
func NewStores(configured Store, alsoRead ...Store) (*Stores, error) {
	stores := &Stores{configured: configured.Name(), byName: map[string]Store{configured.Name(): configured}}
	for _, store := range alsoRead {
		if _, taken := stores.byName[store.Name()]; taken {
			return nil, fmt.Errorf("filestore: two stores are named %q", store.Name())
		}
		stores.byName[store.Name()] = store
	}
	return stores, nil
}

// Configured is the name of the backend the deployment writes new files to.
// It answers one question only -- where the bytes of a record that names no
// backend are, which is where the Python tier looks for them too -- and is
// never a second place to look for a row that does name one.
func (s *Stores) Configured() string { return s.configured }

// Named is the store a row recorded, or ErrBackendNotRead saying which
// backends this process does read.
func (s *Stores) Named(backend string) (Store, error) {
	store, read := s.byName[backend]
	if !read {
		return nil, fmt.Errorf("%w: %s (it reads %s)", ErrBackendNotRead, strconv.Quote(backend), s.names())
	}
	return store, nil
}

// names lists the backends in the set, quoted and in a fixed order, for an
// error message.
func (s *Stores) names() string {
	names := make([]string, 0, len(s.byName))
	for name := range s.byName {
		names = append(names, strconv.Quote(name))
	}
	slices.Sort(names)
	return strings.Join(names, " and ")
}

// Close closes every store in the set and reports every failure.
func (s *Stores) Close() error {
	var failures []error
	for _, store := range s.byName {
		failures = append(failures, store.Close())
	}
	return errors.Join(failures...)
}
