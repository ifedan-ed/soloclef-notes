package filestore

// Tests and benchmarks of the S3 backend against a real MinIO.
//
// They are opt-in: without the three variables below every one of them is
// skipped, so `go test ./...` needs no container. To run them, start a
// throwaway MinIO bound to loopback, under a name of its own, and remove it
// afterwards:
//
//	docker run -d --rm --name soloclef-s3test-$$ -p 127.0.0.1:19000:9000 \
//	    -e MINIO_ROOT_USER=example-access -e MINIO_ROOT_PASSWORD=example-secret \
//	    --tmpfs /data minio/minio server /data
//
//	SOLOCLEF_TEST_S3_ENDPOINT=http://127.0.0.1:19000 \
//	SOLOCLEF_TEST_S3_ACCESS_KEY=example-access \
//	SOLOCLEF_TEST_S3_SECRET_KEY=example-secret \
//	    go test . -run MinIO -bench S3 -benchmem -count=1
//
//	docker rm -f soloclef-s3test-$$
//
// Each test makes a bucket with a random name and deletes it when it ends,
// so a run leaves nothing behind in the store and two runs cannot collide.
// The Python tier's tests read the same three variables
// (tests/integration/file_storage).

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	testEndpointVariable  = "SOLOCLEF_TEST_S3_ENDPOINT"
	testAccessKeyVariable = "SOLOCLEF_TEST_S3_ACCESS_KEY"
	testSecretKeyVariable = "SOLOCLEF_TEST_S3_SECRET_KEY"
	testRegion            = "us-east-1"
)

// minioBucket is one freshly made bucket on the test MinIO: the store under
// test reading it, and a writer to put objects there. The writer is a plain
// minio-go client: it stands in for the Python tier, so the reads are tested
// against objects this package did not write itself.
type minioBucket struct {
	store    *S3
	writer   *minio.Client
	name     string
	settings S3Settings
}

func openMinIOBucket(tb testing.TB) *minioBucket {
	tb.Helper()
	endpoint := os.Getenv(testEndpointVariable)
	accessKey := os.Getenv(testAccessKeyVariable)
	secretKey := os.Getenv(testSecretKeyVariable)
	if endpoint == "" || accessKey == "" || secretKey == "" {
		tb.Skipf("set %s, %s and %s to run against a MinIO (see the top of this file)",
			testEndpointVariable, testAccessKeyVariable, testSecretKeyVariable)
	}
	host, secure, err := s3Endpoint(endpoint)
	if err != nil {
		tb.Fatal(err)
	}
	writer, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:       secure,
		Region:       testRegion,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		tb.Fatal(err)
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		tb.Fatal(err)
	}
	name := "soloclef-test-" + hex.EncodeToString(suffix)
	ctx := context.Background()
	if err := writer.MakeBucket(ctx, name, minio.MakeBucketOptions{Region: testRegion}); err != nil {
		tb.Fatalf("making the test bucket: %v", err)
	}
	tb.Cleanup(func() {
		for object := range writer.ListObjects(ctx, name, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err == nil {
				writer.RemoveObject(ctx, name, object.Key, minio.RemoveObjectOptions{})
			}
		}
		if err := writer.RemoveBucket(ctx, name); err != nil {
			tb.Errorf("removing the test bucket %s: %v", name, err)
		}
	})

	settings := S3Settings{
		Endpoint: endpoint, Bucket: name, Region: testRegion, PathStyle: true,
		AccessKey: accessKey, SecretKey: secretKey,
	}
	store, err := OpenS3(settings)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { store.Close() })
	return &minioBucket{store: store, writer: writer, name: name, settings: settings}
}

// put stores an object with one PUT, as the Python tier's `put` does.
func (m *minioBucket) put(tb testing.TB, key string, content []byte, contentType string) {
	tb.Helper()
	_, err := m.writer.PutObject(context.Background(), m.name, key, bytes.NewReader(content), int64(len(content)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		tb.Fatal(err)
	}
}

// repeated is a reader of `total` bytes: one megabyte of the recognisable
// pattern, over and over. A large object is then never held in memory by the
// test that uploads it, and because the content depends only on a byte's
// position -- not on how the reads happen to be sized -- two of these
// readers always produce the same bytes.
func repeated(total int64) io.Reader {
	return io.LimitReader(&cycle{piece: pattern(1 << 20)}, total)
}

type cycle struct {
	piece    []byte
	position int
}

func (c *cycle) Read(buffer []byte) (int, error) {
	read := copy(buffer, c.piece[c.position:])
	c.position = (c.position + read) % len(c.piece)
	return read, nil
}

func TestMinIOStatOpenAndRange(t *testing.T) {
	bucket := openMinIOBucket(t)
	content := pattern(300_000)
	bucket.put(t, deckKey, content, "application/json")
	ctx := context.Background()

	info, err := bucket.store.Stat(ctx, deckKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(content)) || info.ContentType != "application/json" {
		t.Errorf("size %d type %q", info.Size, info.ContentType)
	}
	if age := time.Since(info.Modified); age < -time.Minute || age > time.Hour {
		t.Errorf("modified %s is not when the object was written", info.Modified)
	}

	stream, err := bucket.store.Open(ctx, deckKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || !bytes.Equal(got, content) {
		t.Errorf("stream: read %d bytes (%v), want %d", len(got), err, len(content))
	}

	for name, span := range map[string]struct{ offset, length int64 }{
		"the first byte":       {0, 1},
		"a span in the middle": {123_456, 7_890},
		"the last byte":        {299_999, 1},
		"the whole object":     {0, 300_000},
		"nothing, at the end":  {300_000, 0},
	} {
		stream, err := bucket.store.OpenRange(ctx, deckKey, span.offset, span.length)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || !bytes.Equal(got, content[span.offset:span.offset+span.length]) {
			t.Errorf("%s: read %d bytes (%v)", name, len(got), err)
		}
	}
}

func TestMinIORangePastTheEndIsRefused(t *testing.T) {
	bucket := openMinIOBucket(t)
	bucket.put(t, deckKey, pattern(1000), "")

	for name, span := range map[string]struct{ offset, length int64 }{
		// MinIO answers this 206 with ten bytes; the store must not.
		"runs one byte past the end":   {990, 11},
		"starts at the end":            {1000, 1},
		"starts beyond the end":        {5000, 10},
		"nothing, from beyond the end": {1001, 0},
	} {
		stream, err := bucket.store.OpenRange(context.Background(), deckKey, span.offset, span.length)
		if !errors.Is(err, ErrRangeNotSatisfiable) {
			t.Errorf("%s: got %v, want ErrRangeNotSatisfiable", name, err)
		}
		if stream != nil {
			stream.Close()
		}
	}
}

func TestMinIOAMissingKeyAndADeletedOne(t *testing.T) {
	bucket := openMinIOBucket(t)
	bucket.put(t, deckKey, pattern(100), "")
	ctx := context.Background()
	const missing = "documents/7/31/original/ffffffffffffffffffffffffffffffff.pdf"

	if _, err := bucket.store.Stat(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat of a key never written: %v", err)
	}
	if _, err := bucket.store.Open(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open of a key never written: %v", err)
	}
	if _, err := bucket.store.OpenRange(ctx, missing, 0, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("OpenRange of a key never written: %v", err)
	}
	// A prefix that real keys share names no object.
	if _, err := bucket.store.Stat(ctx, "artifacts/7"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat of a prefix: %v", err)
	}

	// The Python tier deletes; this tier must then see the row's blob gone.
	if err := bucket.writer.RemoveObject(ctx, bucket.name, deckKey, minio.RemoveObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := bucket.store.Stat(ctx, deckKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat after the delete: %v", err)
	}
	if _, err := bucket.store.Open(ctx, deckKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open after the delete: %v", err)
	}
}

// An object the writer sent as a multipart upload -- which is how the Python
// tier stores anything over eight megabytes -- is read exactly like any
// other: whole, and by ranges that cross the boundaries between its parts.
func TestMinIOABigMultipartObject(t *testing.T) {
	bucket := openMinIOBucket(t)
	const (
		size     = 21<<20 + 12_345 // four full 5 MiB parts and a short fifth
		partSize = 5 << 20
		key      = "artifacts/7/32/primary/fedcba9876543210fedcba9876543210.mp4"
	)
	ctx := context.Background()
	uploaded, err := bucket.writer.PutObject(ctx, bucket.name, key, repeated(size), size,
		minio.PutObjectOptions{PartSize: partSize, ContentType: "video/mp4"})
	if err != nil {
		t.Fatal(err)
	}
	// A multipart object's ETag ends in "-<number of parts>": proof that
	// this test read what it says it read.
	if !strings.HasSuffix(uploaded.ETag, "-5") {
		t.Fatalf("the object was not stored in five parts (ETag %q)", uploaded.ETag)
	}

	info, err := bucket.store.Stat(ctx, key)
	if err != nil || info.Size != size || info.ContentType != "video/mp4" {
		t.Fatalf("Stat: %+v, %v", info, err)
	}

	want := sha256.New()
	io.Copy(want, repeated(size))
	stream, err := bucket.store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got := sha256.New()
	read, err := io.Copy(got, stream)
	stream.Close()
	if err != nil || read != size || !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Errorf("stream: %d bytes (%v), checksum equal: %v", read, err, bytes.Equal(got.Sum(nil), want.Sum(nil)))
	}

	// The whole content again, as a plain slice, to check ranges by position.
	whole, _ := io.ReadAll(repeated(size))
	for name, span := range map[string]struct{ offset, length int64 }{
		"across the first part boundary": {partSize - 1000, 2000},
		"across three parts":             {partSize - 5, 2*partSize + 10},
		"the last byte":                  {size - 1, 1},
		"the short last part":            {4 * partSize, size - 4*partSize},
	} {
		stream, err := bucket.store.OpenRange(ctx, key, span.offset, span.length)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		part, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || !bytes.Equal(part, whole[span.offset:span.offset+span.length]) {
			t.Errorf("%s: read %d bytes (%v)", name, len(part), err)
		}
	}
	if _, err := bucket.store.OpenRange(ctx, key, size-10, 11); !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Errorf("one byte past the end of the multipart object: %v", err)
	}
}

// The set of stores against a real bucket: after the move to s3, a row
// recorded local is read off the disk and a row recorded s3 out of MinIO,
// under one key that holds different bytes in each.
func TestMinIOARowIsReadFromTheBackendItRecorded(t *testing.T) {
	bucket := openMinIOBucket(t)
	const key = "artifacts/7/31/primary/0123456789abcdef0123456789abcdef.json"
	bucket.put(t, key, []byte("in the bucket"), "application/json")
	_, directory := storeWith(t, map[string][]byte{key: []byte("on the disk")})

	stores, err := OpenStores(Settings{Backend: S3Backend, LocalRoot: directory, S3: bucket.settings})
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	for backend, want := range map[string]string{LocalBackend: "on the disk", S3Backend: "in the bucket"} {
		store, err := stores.Named(backend)
		if err != nil {
			t.Fatalf("%s: %v", backend, err)
		}
		stream, err := store.Open(context.Background(), key)
		if err != nil {
			t.Fatalf("%s: %v", backend, err)
		}
		content, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || string(content) != want {
			t.Errorf("a row recorded %s read %q, %v", backend, content, err)
		}
	}

	// Without the directory, the row recorded local is refused -- with the
	// bucket holding its key.
	onlyTheBucket, err := OpenStores(Settings{Backend: S3Backend, S3: bucket.settings})
	if err != nil {
		t.Fatal(err)
	}
	defer onlyTheBucket.Close()
	if _, err := onlyTheBucket.Named(LocalBackend); !errors.Is(err, ErrBackendNotRead) {
		t.Errorf("got %v", err)
	}

	// And a bucket described beside local must be reachable to start.
	wrong := bucket.settings
	wrong.SecretKey = "not-the-secret"
	if refused, err := OpenStores(Settings{Backend: LocalBackend, LocalRoot: directory, S3: wrong}); err == nil {
		refused.Close()
		t.Error("a bucket with a wrong credential, beside local, did not stop the start-up")
	}
}

func TestMinIOStartUpRefusesAWrongCredentialAndAMissingBucket(t *testing.T) {
	bucket := openMinIOBucket(t)

	wrongKey := bucket.settings
	wrongKey.SecretKey = "not-the-secret-key"
	if store, err := OpenS3(wrongKey); err == nil {
		store.Close()
		t.Error("a wrong secret key opened the store")
	} else if strings.Contains(err.Error(), wrongKey.SecretKey) {
		t.Errorf("the error prints the secret key: %v", err)
	}

	noBucket := bucket.settings
	noBucket.Bucket = bucket.name + "-absent"
	if store, err := OpenS3(noBucket); err == nil {
		store.Close()
		t.Error("a bucket that does not exist opened the store")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks against MinIO, shaped exactly like the local ones in
// local_test.go (same object size, same range, same consumers) so the two
// sets of figures can be read side by side.
//
//	go test . -run '^$' -bench . -benchmem
//
// As there, B/op is the number to watch: memory per stream must not grow
// with the object.

func benchmarkBucket(b *testing.B) *S3 {
	b.Helper()
	bucket := openMinIOBucket(b)
	_, err := bucket.writer.PutObject(context.Background(), bucket.name, "big.bin",
		repeated(benchmarkObject), benchmarkObject, minio.PutObjectOptions{PartSize: 16 << 20})
	if err != nil {
		b.Fatal(err)
	}
	return bucket.store
}

// BenchmarkS3StreamWholeObject reads a 256 MiB object end to end into a
// SHA-256, like BenchmarkStreamWholeObject.
func BenchmarkS3StreamWholeObject(b *testing.B) {
	store := benchmarkBucket(b)
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

// BenchmarkS3StreamWholeObjectDiscarded is the store's own cost with the
// consumer taken out.
func BenchmarkS3StreamWholeObjectDiscarded(b *testing.B) {
	store := benchmarkBucket(b)
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

// BenchmarkS3RangeRead is a player seeking: one megabyte from 200 MiB into
// the object. Its cost must be that of one megabyte and one round trip.
func BenchmarkS3RangeRead(b *testing.B) {
	store := benchmarkBucket(b)
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

// BenchmarkS3Stat is one HEAD: the question a ranged request asks first.
func BenchmarkS3Stat(b *testing.B) {
	store := benchmarkBucket(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := store.Stat(context.Background(), "big.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

// The write half, against the same real MinIO.

func TestMinIOPutStreamSendsALargeBodyInPartsAndReportsWhatItStored(t *testing.T) {
	bucket := openMinIOBucket(t)
	// Two parts and a bit: more than s3PartSize, so this is a multipart
	// upload and not the single PUT a small body gets.
	content := make([]byte, 2*s3PartSize+12_345)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stored, err := bucket.store.PutStream(ctx, "documents/1/2/original/large.bin", bytes.NewReader(content), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)
	if stored.Size != int64(len(content)) || stored.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("stored = %+v, want %d bytes and %x", stored, len(content), want)
	}
	info, err := bucket.store.Stat(ctx, "documents/1/2/original/large.bin")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(content)) || info.ContentType != "application/octet-stream" {
		t.Fatalf("info = %+v", info)
	}
	body, err := bucket.store.Open(ctx, "documents/1/2/original/large.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, body); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(digest.Sum(nil), want[:]) {
		t.Fatal("the object read back is not what was streamed in")
	}
}

func TestMinIOPutStreamNeverReplacesAnObject(t *testing.T) {
	bucket := openMinIOBucket(t)
	ctx := context.Background()
	if err := bucket.store.Put(ctx, "a/once.txt", []byte("first"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := bucket.store.Put(ctx, "a/once.txt", []byte("second"), "text/plain"); !errors.Is(err, ErrExists) {
		t.Fatalf("a second single-request write: %v", err)
	}
	// The rule has to hold for a multipart upload too, where it is the
	// completing request that carries the condition.
	large := make([]byte, s3PartSize+1)
	if _, err := bucket.store.PutStream(ctx, "a/once.txt", bytes.NewReader(large), ""); !errors.Is(err, ErrExists) {
		t.Fatalf("a second multipart write: %v", err)
	}
	body, err := bucket.store.Open(ctx, "a/once.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if read, _ := io.ReadAll(body); string(read) != "first" {
		t.Fatalf("the first object was changed to %q", read)
	}
}

func TestMinIOPutStreamLeavesNoObjectWhenTheBodyFails(t *testing.T) {
	bucket := openMinIOBucket(t)
	gone := errors.New("the client went away")
	// More than one part, so a session was open when the body failed.
	half := &failingAfter{data: make([]byte, s3PartSize+1024), err: gone}
	if _, err := bucket.store.PutStream(context.Background(), "a/half.bin", half, ""); !errors.Is(err, gone) {
		t.Fatalf("err = %v, want the body's own failure", err)
	}
	if _, err := bucket.store.Stat(context.Background(), "a/half.bin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("half an upload is readable as the object: %v", err)
	}
	// And no multipart session left open: its parts would be stored, and
	// billed, with nothing naming them.
	for upload := range bucket.writer.ListIncompleteUploads(context.Background(), bucket.name, "", true) {
		t.Fatalf("an unfinished upload was left in the bucket: %+v", upload)
	}
}

func TestMinIORemoveDeletesAndIsRepeatable(t *testing.T) {
	bucket := openMinIOBucket(t)
	ctx := context.Background()
	if err := bucket.store.Put(ctx, "a/gone.txt", []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		if err := bucket.store.Remove(ctx, "a/gone.txt"); err != nil {
			t.Fatalf("remove %d: %v", attempt, err)
		}
	}
	if _, err := bucket.store.Stat(ctx, "a/gone.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a removed object: %v", err)
	}
}

// BenchmarkS3PutStream is what storing a typical upload costs against the
// store: a quarter of a megabyte, which is inside the read-ahead and so one
// PUT with no part buffer.
func BenchmarkS3PutStream(b *testing.B) {
	bucket := openMinIOBucket(b)
	content := bytes.Repeat([]byte{0x5a}, 256<<10)
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		if _, err := bucket.store.PutStream(context.Background(), "bench/"+strconv.Itoa(i)+".bin", bytes.NewReader(content), ""); err != nil {
			b.Fatal(err)
		}
	}
}
