package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 is the "s3" backend: the object for a key is the object with that key in
// one bucket of an S3-compatible store (MinIO, or anything that speaks the
// same API).
//
// # Why minio-go, and why this version
//
// The two candidates were minio-go v7 and aws-sdk-go-v2. This package makes
// three kinds of request -- HEAD, GET, ranged GET -- and minio-go's Core API
// is exactly that: GetObject sends one request and returns the response body
// itself. A custom endpoint and path-style addressing, the two things a
// self-hosted store needs, are plain options there rather than a resolver to
// write, and it is one module where the AWS SDK is about a dozen. The AWS
// SDK would have been the better choice only for AWS-specific features
// (IAM role chains, SigV4a, S3 Express), none of which a self-hosted store
// has.
//
// The version is v7.0.98, not the newest, on purpose: it is the last release
// that builds with this module's Go 1.24. Later ones need Go 1.25 or 1.26,
// which would mean changing the toolchain and the build image for a file
// store.
//
// # One request per read
//
// Open and OpenRange each send a single GET and learn everything they need
// -- the length, and whether the range was satisfied -- from that response's
// own headers. There is no HEAD first. minio-go's convenient Client.GetObject
// is deliberately not used: it returns a lazy, seekable object that stats
// the key and then fetches it, and runs a goroutine per open object to do
// so, which doubles the round trips of every seek in a video.
//
// # Reading only
//
// Like the local backend, this one has no write and no delete. The
// credential the deployment gives this tier needs s3:GetObject on the bucket
// and s3:ListBucket (which is what lets the store answer "no such key"
// instead of "access denied" for a missing one), and nothing else.
type S3 struct {
	core   minio.Core
	bucket string
	// transport is kept only so Close can release its idle connections.
	transport *http.Transport
}

// What OpenS3 allows the start-up check, and how often one request is
// retried. Both are short because both are paid by someone waiting: a
// process that cannot reach its store should say so in seconds, and a
// request for a file should fail rather than hold a connection for the
// minute minio-go's default of ten attempts can take.
const (
	s3StartupTimeout = 10 * time.Second
	s3MaxRetries     = 3
	// How many idle connections are kept to the store. Each concurrent
	// download holds one; minio-go's own default of 16 would have the 17th
	// concurrent viewer of a video open a fresh connection -- and for TLS a
	// fresh handshake -- on every range request.
	s3IdleConnections = 128
)

// OpenS3 connects to the bucket and confirms it can be read. Like OpenLocal
// with a directory that is not there, a bucket that does not exist, a
// credential the store refuses or an endpoint nothing answers on stops the
// process at start-up, rather than turning every later request into a 500.
func OpenS3(settings S3Settings) (*S3, error) {
	host, secure, err := s3Endpoint(settings.Endpoint)
	if err != nil {
		return nil, err
	}
	// Every missing field is reported in one error, so a deployment missing
	// three is told about three.
	var missing []string
	for _, field := range []struct{ name, value string }{
		{"bucket", settings.Bucket},
		{"region", settings.Region},
		{"access key", settings.AccessKey},
		{"secret key", settings.SecretKey},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("filestore: the s3 backend needs its %s", strings.Join(missing, ", "))
	}

	transport, err := minio.DefaultTransport(secure)
	if err != nil {
		return nil, fmt.Errorf("filestore: building the s3 transport: %w", err)
	}
	// The endpoint setting is the address. minio-go's transport would also
	// consult HTTP_PROXY and friends, which is a second, unlisted setting
	// deciding where object requests go.
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = s3IdleConnections

	lookup := minio.BucketLookupDNS
	if settings.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(host, &minio.Options{
		// Static credentials, and only these: nothing is looked up in the
		// environment, in ~/.aws or at an instance-metadata address.
		Creds:        credentials.NewStaticV4(settings.AccessKey, settings.SecretKey, ""),
		Secure:       secure,
		Region:       settings.Region,
		BucketLookup: lookup,
		Transport:    transport,
		MaxRetries:   s3MaxRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("filestore: configuring the s3 store: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), s3StartupTimeout)
	defer cancel()
	exists, err := client.BucketExists(ctx, settings.Bucket)
	if err != nil {
		return nil, fmt.Errorf("filestore: reaching bucket %q at %s: %w", settings.Bucket, settings.Endpoint, err)
	}
	if !exists {
		return nil, fmt.Errorf("filestore: there is no bucket %q at %s", settings.Bucket, settings.Endpoint)
	}
	return &S3{core: minio.Core{Client: client}, bucket: settings.Bucket, transport: transport}, nil
}

// s3Endpoint splits the endpoint setting into the host minio-go wants and
// whether to use TLS, refusing anything that is not exactly a scheme and a
// host. A path is refused rather than ignored: it would otherwise look as if
// "https://host/prefix" stored objects under a prefix, and it would not.
func s3Endpoint(endpoint string) (host string, secure bool, err error) {
	if endpoint == "" {
		return "", false, errors.New("filestore: the s3 backend needs its endpoint")
	}
	parsed, parseErr := url.Parse(endpoint)
	if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false, fmt.Errorf("filestore: the s3 endpoint %q must be http:// or https:// followed by a host", endpoint)
	}
	if strings.Trim(parsed.Path, "/") != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", false, fmt.Errorf("filestore: the s3 endpoint %q must be a scheme and a host only", endpoint)
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

// Name is "s3", as rows record it.
func (s *S3) Name() string { return S3Backend }

// Close drops the idle connections to the store. The transport would time
// them out by itself; closing them now lets a shutdown finish clean. Streams
// still being read are not cut off: they belong to their requests.
func (s *S3) Close() error {
	s.transport.CloseIdleConnections()
	return nil
}

// Stat describes the object with one HEAD.
func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	if err := validKey(key); err != nil {
		return Info{}, err
	}
	stat, err := s.core.Client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return Info{}, s3Failure(key, err)
	}
	return Info{Size: stat.Size, Modified: stat.LastModified, ContentType: stat.ContentType}, nil
}

// Open streams the whole object with one GET.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	body, stat, _, err := s.core.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, s3Failure(key, err)
	}
	return &exactly{body: body, remaining: stat.Size}, nil
}

// OpenRange streams `length` bytes from `offset` with one ranged GET: the
// store sends those bytes and no others.
func (s *S3) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	// The second test is the overflow guard: offset+length-1 must be
	// computable. A range that large is outside any object there could be.
	if offset < 0 || length < 0 || length > math.MaxInt64-offset {
		return nil, ErrRangeNotSatisfiable
	}
	if length == 0 {
		// HTTP has no way to ask for zero bytes, so the only question left
		// is whether the offset is inside the object (its end included, as
		// for the local backend), and a HEAD answers that.
		info, err := s.Stat(ctx, key)
		if err != nil {
			return nil, err
		}
		if offset > info.Size {
			return nil, ErrRangeNotSatisfiable
		}
		return io.NopCloser(strings.NewReader("")), nil
	}

	last := offset + length - 1
	options := minio.GetObjectOptions{}
	if err := options.SetRange(offset, last); err != nil {
		return nil, fmt.Errorf("filestore: asking for a range of %q: %w", key, err)
	}
	body, _, header, err := s.core.GetObject(ctx, s.bucket, key, options)
	if err != nil {
		return nil, s3Failure(key, err)
	}
	// S3 answers a range that runs past the end with the part that exists,
	// and says so only in Content-Range. The contract here is "exactly
	// `length` bytes or ErrRangeNotSatisfiable", so the header is read and
	// held to it -- on the response already in hand, which is what saves
	// the HEAD a check-then-read would cost.
	first, sentLast, ok := contentRange(header.Get("Content-Range"))
	switch {
	case !ok:
		body.Close()
		// A 200 with the whole object: the store ignored the Range header.
		// Streaming that as if it were the range would send wrong bytes.
		return nil, fmt.Errorf("filestore: the store did not answer %q with the range asked for (Content-Range %q)",
			key, header.Get("Content-Range"))
	case first != offset || sentLast != last:
		body.Close()
		return nil, ErrRangeNotSatisfiable
	}
	return &exactly{body: body, remaining: length}, nil
}

// contentRange reads "bytes first-last/total" and returns first and last.
// The total is not needed: it may legitimately be "*".
func contentRange(header string) (first, last int64, ok bool) {
	span, found := strings.CutPrefix(header, "bytes ")
	if !found {
		return 0, 0, false
	}
	span, _, found = strings.Cut(span, "/")
	if !found {
		return 0, 0, false
	}
	firstText, lastText, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	first, firstErr := strconv.ParseInt(firstText, 10, 64)
	last, lastErr := strconv.ParseInt(lastText, 10, 64)
	if firstErr != nil || lastErr != nil || first < 0 || last < first {
		return 0, 0, false
	}
	return first, last, true
}

// s3Failure sorts a store error into the kinds a caller handles, by the same
// rule as the local backend's readFailure: only "there is no such object" is
// ErrNotFound. A refused credential, a missing bucket or an unreachable
// store is returned as itself, because answering "not found" for a store
// that cannot be read would hide an outage behind a 404.
func s3Failure(key string, err error) error {
	// A cancelled request is the caller's own doing and is passed through
	// unwrapped, so errors.Is(err, context.Canceled) keeps working.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	response := minio.ToErrorResponse(err)
	switch {
	case response.Code == minio.NoSuchKey:
		return fmt.Errorf("%w: %q", ErrNotFound, key)
	case response.Code == minio.InvalidRange, response.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return ErrRangeNotSatisfiable
	default:
		return fmt.Errorf("filestore: reading %q: %w", key, err)
	}
}

// exactly is a response body that must deliver `remaining` more bytes.
//
// net/http already reports a body that ends before its Content-Length. This
// wrapper keeps the Store contract -- "a stream that ends early fails with
// io.ErrUnexpectedEOF rather than ending quietly short" -- true on its own
// terms as well, whatever a proxy between this tier and the store does to
// the framing, at the cost of one comparison per read.
type exactly struct {
	body      io.ReadCloser
	remaining int64
}

func (e *exactly) Read(buffer []byte) (int, error) {
	read, err := e.body.Read(buffer)
	e.remaining -= int64(read)
	if err == io.EOF && e.remaining > 0 {
		return read, io.ErrUnexpectedEOF
	}
	return read, err
}

// Close ends the request. A body closed before its end is not drained: for a
// viewer who seeks away from a two-gigabyte video, reading the rest to keep
// one connection would cost far more than opening another.
func (e *exactly) Close() error { return e.body.Close() }
