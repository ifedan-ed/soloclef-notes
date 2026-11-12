package filestore

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sameKey is one key held by both stores with different bytes, so that what
// a read returns says which store answered.
const sameKey = "artifacts/7/31/primary/0123456789abcdef0123456789abcdef.json"

// bothBackends is a directory and a fake bucket, each holding sameKey, and
// the settings that describe both.
func bothBackends(t *testing.T) (Settings, *fakeBucket) {
	t.Helper()
	_, directory := storeWith(t, map[string][]byte{sameKey: []byte("on the disk")})
	_, bucket := s3StoreWith(t, map[string][]byte{sameKey: []byte("in the bucket")})
	server := httptest.NewServer(bucket)
	t.Cleanup(server.Close)
	return Settings{LocalRoot: directory, S3: fakeSettings(server.URL)}, bucket
}

func contentOf(t *testing.T, stores *Stores, backend string) string {
	t.Helper()
	store, err := stores.Named(backend)
	if err != nil {
		t.Fatalf("%s: %v", backend, err)
	}
	if store.Name() != backend {
		t.Fatalf("asked for %q, got a store named %q", backend, store.Name())
	}
	stream, err := store.Open(context.Background(), sameKey)
	if err != nil {
		t.Fatalf("%s: %v", backend, err)
	}
	defer stream.Close()
	content, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("%s: %v", backend, err)
	}
	return string(content)
}

// The move from local to s3, and the move back: whichever backend is the
// configured one, a row recorded in the other is read from the other,
// because its settings were given.
func TestBothBackendsAreReadWhenBothAreDescribed(t *testing.T) {
	for _, configured := range []string{S3Backend, LocalBackend} {
		t.Run("configured "+configured, func(t *testing.T) {
			settings, _ := bothBackends(t)
			settings.Backend = configured
			stores, err := OpenStores(settings)
			if err != nil {
				t.Fatal(err)
			}
			defer stores.Close()

			if stores.Configured() != configured {
				t.Errorf("the configured backend is reported as %q", stores.Configured())
			}
			if got := contentOf(t, stores, LocalBackend); got != "on the disk" {
				t.Errorf("a row recorded local read %q", got)
			}
			if got := contentOf(t, stores, S3Backend); got != "in the bucket" {
				t.Errorf("a row recorded s3 read %q", got)
			}
		})
	}
}

// A backend is in the set only if its settings were given. The other one
// being right there, holding an object under the same key, changes nothing:
// it is never where a row recorded elsewhere is looked for.
func TestABackendWithNoSettingsIsNotRead(t *testing.T) {
	both, bucket := bothBackends(t)
	for name, test := range map[string]struct {
		settings Settings
		absent   string
		reads    string
	}{
		"s3 with no directory": {Settings{Backend: S3Backend, S3: both.S3}, LocalBackend, `(it reads "s3")`},
		"local with no bucket": {Settings{Backend: LocalBackend, LocalRoot: both.LocalRoot}, S3Backend, `(it reads "local")`},
	} {
		t.Run(name, func(t *testing.T) {
			stores, err := OpenStores(test.settings)
			if err != nil {
				t.Fatal(err)
			}
			defer stores.Close()
			asked := bucket.count()

			store, err := stores.Named(test.absent)
			if !errors.Is(err, ErrBackendNotRead) || store != nil {
				t.Fatalf("got %v, %v", store, err)
			}
			if !strings.Contains(err.Error(), `"`+test.absent+`"`) || !strings.Contains(err.Error(), test.reads) {
				t.Errorf("the error does not say what is missing and what is read: %v", err)
			}
			if bucket.count() != asked {
				t.Errorf("refusing a backend asked the bucket something: %v", bucket.since(asked))
			}
		})
	}
}

// A name that is no backend is refused whatever is configured, and so are
// near-misses of a real one: a row's record is matched exactly.
func TestARowNamingNoBackendThereIsIsRefused(t *testing.T) {
	settings, _ := bothBackends(t)
	settings.Backend = S3Backend
	stores, err := OpenStores(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	for _, recorded := range []string{"gcs", "", "S3", "local ", "s3,local"} {
		store, err := stores.Named(recorded)
		if !errors.Is(err, ErrBackendNotRead) || store != nil {
			t.Errorf("%q: got %v, %v", recorded, store, err)
			continue
		}
		if !strings.Contains(err.Error(), `(it reads "local" and "s3")`) {
			t.Errorf("%q: the error does not list what is read: %v", recorded, err)
		}
	}
}

// Whatever is described must open. A second backend that cannot be opened
// stops the start-up; it is not left out of the set for its rows to be
// refused one by one later.
func TestOpenStoresRefusesSettingsItCannotOpen(t *testing.T) {
	both, _ := bothBackends(t)
	halfABucket := S3Settings{Endpoint: both.S3.Endpoint, Bucket: both.S3.Bucket}
	noSuchBucket := both.S3
	noSuchBucket.Bucket = "absent-bucket"
	for name, test := range map[string]struct {
		settings Settings
		says     string
	}{
		"no backend named, both described": {
			Settings{LocalRoot: both.LocalRoot, S3: both.S3}, "no storage backend is named"},
		"a backend that does not exist, both described": {
			Settings{Backend: "gcs", LocalRoot: both.LocalRoot, S3: both.S3}, `"gcs" is not a storage backend`},
		"local with no directory, a bucket described": {
			Settings{Backend: LocalBackend, S3: both.S3}, "the local backend needs the directory"},
		"s3 with no bucket, a directory described": {
			Settings{Backend: S3Backend, LocalRoot: both.LocalRoot}, "the s3 backend needs its endpoint"},
		"local, beside half a bucket": {
			Settings{Backend: LocalBackend, LocalRoot: both.LocalRoot, S3: halfABucket},
			"the s3 backend needs its region, access key, secret key"},
		"local, beside a bucket that is not there": {
			Settings{Backend: LocalBackend, LocalRoot: both.LocalRoot, S3: noSuchBucket}, `no bucket "absent-bucket"`},
		"local, beside nothing but a path style": {
			Settings{Backend: LocalBackend, LocalRoot: both.LocalRoot, S3: S3Settings{PathStyle: true}},
			"the s3 backend needs its endpoint"},
		"s3, beside a directory that is not there": {
			Settings{Backend: S3Backend, LocalRoot: filepath.Join(both.LocalRoot, "absent"), S3: both.S3},
			"opening the local store"},
	} {
		t.Run(name, func(t *testing.T) {
			stores, err := OpenStores(test.settings)
			if err == nil {
				stores.Close()
				t.Fatal("want an error, got a set of stores")
			}
			if stores != nil {
				t.Errorf("a failed OpenStores returned a non-nil set")
			}
			if !strings.Contains(err.Error(), test.says) {
				t.Errorf("the error does not say %q: %v", test.says, err)
			}
		})
	}
}

// The volume being mounted is checked per read, for the second backend as
// for the first: a directory that goes away is an outage, not "not found".
func TestTheLocalStoreBesideABucketStillReportsAnOutage(t *testing.T) {
	settings, _ := bothBackends(t)
	settings.Backend = S3Backend
	stores, err := OpenStores(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	if err := os.RemoveAll(settings.LocalRoot); err != nil {
		t.Fatal(err)
	}
	local, err := stores.Named(LocalBackend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), sameKey); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("a store whose directory is gone answered %v", err)
	}
}

func TestNewStoresRefusesTwoStoresUnderOneName(t *testing.T) {
	first, _ := storeWith(t, nil)
	second, _ := storeWith(t, nil)
	if stores, err := NewStores(first, second); err == nil || !strings.Contains(err.Error(), `two stores are named "local"`) {
		t.Errorf("got %v, %v", stores, err)
	}
	stores, err := NewStores(first)
	if err != nil {
		t.Fatal(err)
	}
	if stores.Configured() != LocalBackend {
		t.Errorf("configured: %q", stores.Configured())
	}
}
