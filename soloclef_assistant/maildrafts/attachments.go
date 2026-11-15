package maildrafts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"sync/atomic"
)

// AttachmentFiles reads a draft's files where they live (mailattachments).
type AttachmentFiles interface {
	// Open streams one file's bytes, read as the owner: a source that is
	// no longer the owner's, or no longer there, is an error.
	Open(ctx context.Context, ownerID string, attachment Attachment) (io.ReadCloser, error)
	// Changed is the name of the first of the files whose source no longer
	// holds the file the draft was made with (removed, or replaced by
	// another), "" when none has. It is the cheap look Confirm takes before
	// it issues a go-ahead, from the rows that describe the files; the
	// bytes themselves are checked as they are sent (verifiedFile).
	Changed(ctx context.Context, ownerID string, attachments []Attachment) (string, error)
}

// NoAttachmentFiles is the AttachmentFiles of a guard whose drafts carry
// no files (a test's): any draft with one is refused at Confirm and at
// Send. Named, and passed on purpose, as NoNotifications is.
type NoAttachmentFiles struct{}

func (NoAttachmentFiles) Open(context.Context, string, Attachment) (io.ReadCloser, error) {
	return nil, errors.New("maildrafts: this guard reads no attachment files")
}

func (NoAttachmentFiles) Changed(_ context.Context, _ string, attachments []Attachment) (string, error) {
	if len(attachments) > 0 {
		return attachments[0].Name, nil
	}
	return "", nil
}

// errFileChanged is what a verified stream returns in place of its end
// when the bytes were not the confirmed ones.
var errFileChanged = errors.New("maildrafts: the file is not the one the draft was confirmed with")

// verifiedFile is a file's stream that ends only if it was exactly the
// bytes confirmed: exactly Size of them, with the SHA-256 the draft holds.
// Anything else ends the stream with errFileChanged where the end would be,
// so a consumer (the upload) fails instead of completing, and Changed
// reports it afterwards for the owner's sentence. The bytes pass through
// once; nothing is held.
type verifiedFile struct {
	source  io.ReadCloser
	want    Attachment
	digest  hash.Hash
	read    int64
	changed atomic.Bool
	// whole says the stream reached its end with the confirmed bytes.
	whole atomic.Bool
}

func verified(source io.ReadCloser, want Attachment) *verifiedFile {
	return &verifiedFile{source: source, want: want, digest: sha256.New()}
}

func (v *verifiedFile) Read(buffer []byte) (int, error) {
	n, err := v.source.Read(buffer)
	v.read += int64(n)
	v.digest.Write(buffer[:n])
	if v.read > v.want.Size {
		v.changed.Store(true)
		return n, errFileChanged
	}
	if errors.Is(err, io.EOF) {
		if v.read != v.want.Size || hex.EncodeToString(v.digest.Sum(nil)) != v.want.SHA256 {
			v.changed.Store(true)
			return n, errFileChanged
		}
		v.whole.Store(true)
	}
	return n, err
}

// Close finishes the check and releases the source. An HTTP upload with a
// stated length reads its body to the end to check the length, which is
// where the digest is compared; should a consumer stop at the stated
// length instead, its end is read here before the source is closed, so
// whether the file was the confirmed one (confirmed) never rests on a
// stream half-checked. A stream given up part way (a failed upload) is not
// read on: it was not sent, whatever it holds. Close's error is the
// verdict: errFileChanged for bytes that were not the confirmed ones,
// which the upload's caller (jmapmail) takes as the upload having failed.
func (v *verifiedFile) Close() error {
	if !v.whole.Load() && !v.changed.Load() && v.read == v.want.Size {
		_, _ = io.Copy(io.Discard, v)
	}
	closed := v.source.Close()
	if v.changed.Load() || (v.read == v.want.Size && !v.whole.Load()) {
		return errFileChanged
	}
	return closed
}

// wasChanged reports that the stream was found not to be the confirmed
// file.
func (v *verifiedFile) wasChanged() bool { return v.changed.Load() }
