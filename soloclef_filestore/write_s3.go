package filestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

// s3PartSize is how much of a stream is sent per request.
//
// The length of an upload is not known when it starts (it is a request body
// being read), and for a stream of unknown length minio-go would otherwise
// size its parts for the largest object S3 allows, which is a buffer of
// hundreds of megabytes per upload. Sixteen mebibytes keeps a worker's memory
// per upload small and still allows an object of 160 GB (S3 allows 10,000
// parts), which is far above any file this product accepts. A body shorter
// than one part is sent as a single PUT, with no multipart session at all.
const s3PartSize = 16 << 20

// s3ReadAhead is how much of a body is read before deciding between one PUT
// and a multipart upload; see PutStream.
const s3ReadAhead = 1 << 20

// PutStream stores the body in the bucket: one PUT when it fits in a part,
// and a multipart upload otherwise.
//
// A multipart upload is the bucket's own form of "whole or not at all": the
// parts are invisible until the last request completes them, and minio-go
// aborts the session when the body or the context fails, so a failed upload
// leaves no object and no parts being billed.
//
// "If-None-Match: *" makes the store refuse a key that exists, which is the
// same once-only rule the local backend keeps with a hard link.
func (s *S3) PutStream(ctx context.Context, key string, body io.Reader, contentType string) (Stored, error) {
	if err := validKey(key); err != nil {
		return Stored{}, err
	}
	options := minio.PutObjectOptions{
		ContentType: contentType,
		PartSize:    s3PartSize,
		// One part at a time: the parts come off one stream in order, and
		// more uploaders would only mean more part buffers held at once.
		NumThreads: 1,
	}
	options.SetMatchETagExcept("*")
	counted := measure(body)

	// Read ahead a little before choosing how to send. Told the length is
	// unknown, minio-go allocates a whole part buffer (16 MiB) even for a
	// two-kilobyte note, and most of what this product stores is small
	// (measured: 17 MB allocated per one-megabyte upload without this, 1 MB
	// with it). A body that ends inside the read-ahead is sent with its
	// length, which needs no buffer beyond the read-ahead itself.
	ahead := make([]byte, s3ReadAhead)
	read, err := io.ReadFull(counted, ahead)
	var send io.Reader
	size := int64(-1)
	switch {
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		send, size = bytes.NewReader(ahead[:read]), int64(read)
	case err != nil:
		return Stored{}, fmt.Errorf("filestore: writing %q: %w", key, err)
	default:
		send = io.MultiReader(bytes.NewReader(ahead), counted)
	}
	if _, err := s.core.Client.PutObject(ctx, s.bucket, key, send, size, options); err != nil {
		if minio.ToErrorResponse(err).Code == "PreconditionFailed" {
			return Stored{}, fmt.Errorf("%w: %q", ErrExists, key)
		}
		return Stored{}, fmt.Errorf("filestore: writing %q: %w", key, err)
	}
	return counted.stored(), nil
}

// Remove deletes the object. S3 answers success for a key that names
// nothing, which is the answer this method promises.
func (s *S3) Remove(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if err := s.core.Client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("filestore: removing %q: %w", key, err)
	}
	return nil
}
