package filestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBucket is a hand-written S3 endpoint, just large enough for what this
// package asks of one: HEAD on the bucket, and HEAD and GET (with Range) on
// a key, addressed path-style. It lets the S3 backend's own logic -- which
// errors become which, what a clamped range turns into, how many requests a
// read costs -- be tested in every run, with no container. The same
// behaviour is checked against a real MinIO in s3_minio_test.go.
type fakeBucket struct {
	name     string
	objects  map[string][]byte
	modified time.Time

	// Faults a test can switch on.
	ignoreRange bool // answer a ranged GET with 200 and the whole object
	cutShort    bool // promise the full length, send half, drop the connection
	denied      bool // answer everything 403 AccessDenied

	mu       sync.Mutex
	requests []string // "METHOD /path", in order
}

func (f *fakeBucket) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, request.Method+" "+request.URL.Path)
	f.mu.Unlock()

	if f.denied {
		s3Error(response, http.StatusForbidden, "AccessDenied")
		return
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(request.URL.Path, "/"), "/")
	if bucket != f.name {
		s3Error(response, http.StatusNotFound, "NoSuchBucket")
		return
	}
	if key == "" {
		response.WriteHeader(http.StatusOK)
		return
	}
	content, stored := f.objects[key]
	if !stored {
		s3Error(response, http.StatusNotFound, "NoSuchKey")
		return
	}
	response.Header().Set("Content-Type", "application/x-example")
	response.Header().Set("ETag", `"0123456789abcdef0123456789abcdef"`)
	switch {
	case f.ignoreRange:
		request.Header.Del("Range")
	case f.cutShort:
		response.Header().Set("Content-Length", fmt.Sprint(len(content)))
		response.Header().Set("Last-Modified", f.modified.Format(http.TimeFormat))
		response.WriteHeader(http.StatusOK)
		response.Write(content[:len(content)/2])
		// Hijacking and closing is how a connection dies mid-body.
		connection, _, err := response.(http.Hijacker).Hijack()
		if err == nil {
			connection.Close()
		}
		return
	}
	// ServeContent implements Range the way S3 does where it matters here:
	// 206 with Content-Range, an end past the object clamped, and 416 for a
	// start at or past the end.
	http.ServeContent(response, request, "", f.modified, bytes.NewReader(content))
}

func s3Error(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(status)
	fmt.Fprintf(response, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

// since returns the requests made after the store was opened (OpenS3's own
// bucket check is the first).
func (f *fakeBucket) since(start int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests[start:]...)
}

func (f *fakeBucket) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

const fakeBucketName = "example-bucket"

// s3StoreWith is an S3 store reading a fake bucket that holds the given
// objects.
func s3StoreWith(t testing.TB, objects map[string][]byte) (*S3, *fakeBucket) {
	t.Helper()
	bucket := &fakeBucket{
		name:     fakeBucketName,
		objects:  objects,
		modified: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
	}
	server := httptest.NewServer(bucket)
	t.Cleanup(server.Close)
	store, err := OpenS3(fakeSettings(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, bucket
}

func fakeSettings(endpoint string) S3Settings {
	return S3Settings{
		Endpoint:  endpoint,
		Bucket:    fakeBucketName,
		Region:    "us-east-1",
		PathStyle: true,
		AccessKey: "example-access-key",
		SecretKey: "example-secret-key",
	}
}

func TestS3IsNamedAsRowsRecordIt(t *testing.T) {
	store, _ := s3StoreWith(t, nil)
	if store.Name() != "s3" {
		t.Errorf("name %q, want the value the Python tier writes in storage_backend", store.Name())
	}
}

func TestS3StatDescribesAnObjectWithOneHead(t *testing.T) {
	store, bucket := s3StoreWith(t, map[string][]byte{deckKey: pattern(1234)})
	opened := bucket.count()

	info, err := store.Stat(context.Background(), deckKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 1234 || !info.Modified.Equal(bucket.modified) {
		t.Errorf("size %d modified %s", info.Size, info.Modified)
	}
	// Whatever the store recorded is passed on; nothing is guessed.
	if info.ContentType != "application/x-example" {
		t.Errorf("content type %q", info.ContentType)
	}
	if got := bucket.since(opened); len(got) != 1 || !strings.HasPrefix(got[0], "HEAD ") {
		t.Errorf("requests %v, want one HEAD", got)
	}
}

func TestS3OpenStreamsTheWholeObjectWithOneGet(t *testing.T) {
	content := pattern(300_000)
	store, bucket := s3StoreWith(t, map[string][]byte{deckKey: content})
	opened := bucket.count()

	stream, err := store.Open(context.Background(), deckKey)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, content) {
		t.Errorf("read %d bytes (%v), want the %d that were stored", len(got), err, len(content))
	}
	// No HEAD before the GET: a read is one round trip.
	if requests := bucket.since(opened); len(requests) != 1 || !strings.HasPrefix(requests[0], "GET ") {
		t.Errorf("requests %v, want one GET", requests)
	}
}

func TestS3OpenRangeReadsExactlyTheBytesAskedFor(t *testing.T) {
	content := pattern(10_000)
	store, bucket := s3StoreWith(t, map[string][]byte{deckKey: content})

	for name, span := range map[string]struct{ offset, length int64 }{
		"the first byte":       {0, 1},
		"a span in the middle": {500, 500},
		"the last byte":        {9_999, 1},
		"the last hundred":     {9_900, 100},
		"the whole object":     {0, 10_000},
	} {
		opened := bucket.count()
		stream, err := store.OpenRange(context.Background(), deckKey, span.offset, span.length)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || !bytes.Equal(got, content[span.offset:span.offset+span.length]) {
			t.Errorf("%s: read %d bytes (%v)", name, len(got), err)
		}
		if requests := bucket.since(opened); len(requests) != 1 || !strings.HasPrefix(requests[0], "GET ") {
			t.Errorf("%s: requests %v, want one GET", name, requests)
		}
	}
}

func TestS3OpenRangeRefusesARangeNotWhollyInsideTheObject(t *testing.T) {
	store, _ := s3StoreWith(t, map[string][]byte{deckKey: pattern(1000)})

	for name, span := range map[string]struct{ offset, length int64 }{
		// S3 would answer this one 206 with the 10 bytes that exist. The
		// contract is all of the range or a refusal, never a short answer.
		"runs one byte past the end":      {990, 11},
		"starts at the end":               {1000, 1},
		"starts beyond the end":           {5000, 10},
		"nothing, from beyond the end":    {1001, 0},
		"a negative offset":               {-1, 10},
		"a negative length":               {0, -1},
		"a length that overflows a int64": {10, math.MaxInt64},
	} {
		stream, err := store.OpenRange(context.Background(), deckKey, span.offset, span.length)
		if !errors.Is(err, ErrRangeNotSatisfiable) {
			t.Errorf("%s: got %v, want ErrRangeNotSatisfiable", name, err)
		}
		if stream != nil {
			stream.Close()
		}
	}
}

func TestS3AnEmptyRangeAtTheEndOfTheObjectIsEmptyNotAnError(t *testing.T) {
	// The local backend allows it (zero bytes from offset == size), so this
	// one does: the contract is the same whichever store holds the file.
	store, _ := s3StoreWith(t, map[string][]byte{deckKey: pattern(1000)})

	for _, offset := range []int64{0, 400, 1000} {
		stream, err := store.OpenRange(context.Background(), deckKey, offset, 0)
		if err != nil {
			t.Errorf("offset %d: %v", offset, err)
			continue
		}
		got, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || len(got) != 0 {
			t.Errorf("offset %d: read %d bytes (%v), want none", offset, len(got), err)
		}
	}
}

func TestS3AMissingKeyIsNotFoundFromEveryRead(t *testing.T) {
	store, _ := s3StoreWith(t, map[string][]byte{deckKey: pattern(10)})
	const missing = "documents/7/31/original/ffffffffffffffffffffffffffffffff.pdf"
	ctx := context.Background()

	if _, err := store.Stat(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat: %v", err)
	}
	if _, err := store.Open(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open: %v", err)
	}
	if _, err := store.OpenRange(ctx, missing, 0, 5); !errors.Is(err, ErrNotFound) {
		t.Errorf("OpenRange: %v", err)
	}
	if _, err := store.OpenRange(ctx, missing, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("OpenRange of nothing: %v", err)
	}
	// "artifacts/7" is a prefix other keys share. It names no object.
	if _, err := store.Open(ctx, "artifacts/7"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a prefix: %v", err)
	}
}

func TestS3RefusesWhatIsNotAKeyWithoutAskingTheStore(t *testing.T) {
	store, bucket := s3StoreWith(t, map[string][]byte{deckKey: pattern(10)})
	opened := bucket.count()
	ctx := context.Background()

	// The same texts the local backend refuses. In a bucket "a/../b" would
	// simply be another object name, which is exactly why it is refused: a
	// key must mean one thing on both backends.
	for _, key := range []string{"", ".", "/etc/passwd", "../outside", "artifacts/../../etc/passwd",
		"artifacts//7", "artifacts/7/", "artifacts/./7"} {
		if _, err := store.Stat(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Stat(%q): %v", key, err)
		}
		if _, err := store.Open(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Open(%q): %v", key, err)
		}
		if _, err := store.OpenRange(ctx, key, 0, 1); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("OpenRange(%q): %v", key, err)
		}
	}
	if requests := bucket.since(opened); len(requests) != 0 {
		t.Errorf("the store was asked about a non-key: %v", requests)
	}
}

// A store that ignores Range answers 200 with the whole object. Streaming
// that as bytes 500-999 would hand the caller the wrong bytes with a
// straight face.
func TestS3AStoreThatIgnoresTheRangeIsAnErrorNotTheWholeObject(t *testing.T) {
	store, bucket := s3StoreWith(t, map[string][]byte{deckKey: pattern(1000)})
	bucket.ignoreRange = true

	stream, err := store.OpenRange(context.Background(), deckKey, 500, 100)
	if err == nil {
		stream.Close()
		t.Fatal("want an error, got a stream")
	}
	if errors.Is(err, ErrRangeNotSatisfiable) || errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want a fault of the store, not a verdict on the request", err)
	}
}

func TestS3AStreamCutShortFailsLoudly(t *testing.T) {
	store, bucket := s3StoreWith(t, map[string][]byte{deckKey: pattern(200_000)})
	bucket.cutShort = true

	stream, err := store.Open(context.Background(), deckKey)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	got, err := io.ReadAll(stream)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("read %d bytes and got %v, want io.ErrUnexpectedEOF", len(got), err)
	}
}

// A refused credential is an outage, and must not be reported as a missing
// file: a route would answer 404 and nobody would look at the store.
func TestS3ARefusalIsNotReportedAsNotFound(t *testing.T) {
	store, bucket := s3StoreWith(t, map[string][]byte{deckKey: pattern(10)})
	bucket.denied = true
	ctx := context.Background()

	_, statErr := store.Stat(ctx, deckKey)
	_, openErr := store.Open(ctx, deckKey)
	_, rangeErr := store.OpenRange(ctx, deckKey, 0, 5)
	for name, err := range map[string]error{"Stat": statErr, "Open": openErr, "OpenRange": rangeErr} {
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRangeNotSatisfiable) {
			t.Errorf("%s: got %v, want the store's own failure", name, err)
		}
	}
}

func TestS3ACancelledRequestIsReportedAsCancelled(t *testing.T) {
	store, _ := s3StoreWith(t, map[string][]byte{deckKey: pattern(10)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Open(ctx, deckKey); !errors.Is(err, context.Canceled) {
		t.Errorf("Open: got %v, want context.Canceled", err)
	}
	if _, err := store.Stat(ctx, deckKey); !errors.Is(err, context.Canceled) {
		t.Errorf("Stat: got %v, want context.Canceled", err)
	}
}

func TestOpenS3RefusesSettingsItCannotUse(t *testing.T) {
	complete := fakeSettings("http://objects.example.internal:9000")
	for name, test := range map[string]struct {
		change func(*S3Settings)
		want   string
	}{
		"no endpoint":                         {func(s *S3Settings) { s.Endpoint = "" }, "endpoint"},
		"an endpoint with no scheme":          {func(s *S3Settings) { s.Endpoint = "objects.example.internal:9000" }, "http:// or https://"},
		"an endpoint that is not http":        {func(s *S3Settings) { s.Endpoint = "ftp://objects.example.internal" }, "http:// or https://"},
		"an endpoint with a path":             {func(s *S3Settings) { s.Endpoint = "https://objects.example.com/prefix" }, "scheme and a host only"},
		"an endpoint with a credential in it": {func(s *S3Settings) { s.Endpoint = "https://user:pw@objects.example.com" }, "scheme and a host only"},
		"no bucket":                           {func(s *S3Settings) { s.Bucket = "" }, "bucket"},
		"no region":                           {func(s *S3Settings) { s.Region = "" }, "region"},
		"no access key":                       {func(s *S3Settings) { s.AccessKey = "" }, "access key"},
		"no secret key":                       {func(s *S3Settings) { s.SecretKey = "" }, "secret key"},
	} {
		settings := complete
		test.change(&settings)
		store, err := OpenS3(settings)
		if err == nil {
			store.Close()
			t.Errorf("%s: want an error, got a store", name)
			continue
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: %v does not mention %q", name, err, test.want)
		}
		// The secret is never part of a message.
		if strings.Contains(err.Error(), complete.SecretKey) {
			t.Errorf("%s: the error prints the secret key: %v", name, err)
		}
	}
}

func TestOpenS3ReportsEveryMissingFieldAtOnce(t *testing.T) {
	_, err := OpenS3(S3Settings{Endpoint: "http://objects.example.internal:9000"})
	if err == nil {
		t.Fatal("want an error, got none")
	}
	for _, field := range []string{"bucket", "region", "access key", "secret key"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("%q is not reported: %v", field, err)
		}
	}
}

// Like a local store whose volume is not mounted: a bucket that is not
// there stops the process at start-up instead of failing every request.
func TestOpenS3RefusesABucketThatDoesNotExist(t *testing.T) {
	server := httptest.NewServer(&fakeBucket{name: "some-other-bucket"})
	defer server.Close()

	store, err := OpenS3(fakeSettings(server.URL))
	if err == nil {
		store.Close()
		t.Fatal("want an error, got a store")
	}
	if !strings.Contains(err.Error(), fakeBucketName) {
		t.Errorf("the error does not name the bucket: %v", err)
	}
}

func TestOpenS3RefusesACredentialTheStoreRefuses(t *testing.T) {
	server := httptest.NewServer(&fakeBucket{name: fakeBucketName, denied: true})
	defer server.Close()

	if store, err := OpenS3(fakeSettings(server.URL)); err == nil {
		store.Close()
		t.Fatal("want an error, got a store")
	}
}

// The package's front door: the one setting chooses, and nothing else does.
func TestOpenBuildsTheBackendTheSettingNames(t *testing.T) {
	server := httptest.NewServer(&fakeBucket{name: fakeBucketName})
	defer server.Close()
	directory := t.TempDir()

	// Both backends are fully configured in each case, so the only thing
	// that can decide is Backend.
	both := Settings{LocalRoot: directory, S3: fakeSettings(server.URL)}
	for _, name := range []string{LocalBackend, S3Backend} {
		settings := both
		settings.Backend = name
		store, err := Open(settings)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if store.Name() != name {
			t.Errorf("asked for %q, got a store named %q", name, store.Name())
		}
		store.Close()
	}
}

func TestOpenDoesNotFallBackToLocalWhenS3CannotBeOpened(t *testing.T) {
	// A usable local directory is right there. It must not be used.
	store, err := Open(Settings{Backend: S3Backend, LocalRoot: t.TempDir()})
	if err == nil {
		t.Fatalf("want an error, got a store named %q", store.Name())
	}
	if store != nil {
		t.Errorf("a failed Open returned a non-nil store")
	}
}

func TestOpenNamesBothBackendsWhenTheNameIsUnknown(t *testing.T) {
	_, err := Open(Settings{Backend: "gcs", LocalRoot: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), `"gcs"`) ||
		!strings.Contains(err.Error(), `"local"`) || !strings.Contains(err.Error(), `"s3"`) {
		t.Errorf("got %v", err)
	}
}
