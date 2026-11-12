package drivesync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivefile"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// disposition is what a sync does about one file whose document exists.
type disposition int

const (
	// leave: the stored content is what Drive has.
	leave disposition = iota
	// rename: the content is the same and the file's name is not.
	rename
	// fetch: the document is owed its file, or Drive's content is not
	// what is stored.
	fetch
)

// decide is the whole rule for a file Drive has and the account has a
// document for.
func decide(document Document, file remoteFile) disposition {
	switch {
	case document.State == "deleting":
		return leave
	case document.owed():
		return fetch
	case !sameContent(document, file):
		return fetch
	case document.Name != "" && document.Name != file.Name:
		return rename
	}
	return leave
}

// sameContent reports whether Drive's content is what was last stored. A
// file with bytes carries their MD5; a Google-native one has none, and its
// modification time is all there is. With nothing to compare (a document
// that never had a file stored), it is not the same.
func sameContent(document Document, file remoteFile) bool {
	if file.MD5 != "" || document.MD5 != "" {
		return file.MD5 != "" && file.MD5 == document.MD5
	}
	return file.Modified != "" && file.Modified == document.Modified
}

// errTooLarge is what a capped download's reader returns once the file has
// passed the limit.
var errTooLarge = errors.New("drivesync: the file is larger than the limit")

// capped reads at most limit bytes of a download and then fails. Drive's
// metadata gives a size for a file with bytes and none for an export, so
// the bytes themselves are what is checked.
type capped struct {
	body  io.Reader
	limit int64
	read  int64
}

func (c *capped) Read(buffer []byte) (int, error) {
	// One byte past the limit, to tell a file of exactly the limit from a
	// larger one.
	if room := c.limit + 1 - c.read; int64(len(buffer)) > room {
		buffer = buffer[:room]
	}
	read, err := c.body.Read(buffer)
	c.read += int64(read)
	if c.read > c.limit {
		return 0, errTooLarge
	}
	return read, err
}

// recording remembers the first error its body returned, so a failed write
// can be told apart: the download breaking off, or the store refusing.
type recording struct {
	body   io.Reader
	failed error
}

func (r *recording) Read(buffer []byte) (int, error) {
	read, err := r.body.Read(buffer)
	if err != nil && err != io.EOF && r.failed == nil {
		r.failed = err
	}
	return read, err
}

// markTimeout bounds the statements that record a download's end. They run
// outside the job's context (a job whose deadline has passed must still
// tell its document), so they need a limit of their own.
const markTimeout = 15 * time.Second

// one is one job's work on one connection: the account, the connection,
// and Drive with the job's token.
type one struct {
	job         *Job
	userID      string
	connectorID int64
	drive       drive
}

// fetch brings one file's bytes into the store and hands its document to
// the indexer. The document is waiting for its download when this is
// called. The error is for the log and the job's count; the document
// itself has been told why.
func (o one) fetch(ctx context.Context, document Document, file remoteFile) error {
	kept, storable := drivefile.StoredAs(file.Name, file.MimeType)
	if !storable {
		return o.fail(ctx, document, reasonKind, fmt.Errorf("a %s cannot be stored", file.MimeType))
	}
	body, err := o.drive.open(ctx, file.ID, kept.ExportAs)
	if err != nil {
		return o.fail(ctx, document, reasonFor(err), err)
	}
	defer body.Close()

	key, err := filestore.OriginalKey(o.userID, document.ID, kept.Name)
	if err != nil {
		return o.fail(ctx, document, reasonStorage, err)
	}
	// The download is the store's body: nothing is held between the two,
	// and the store counts and digests the bytes as it writes them. A body
	// that fails partway leaves no object under the key.
	source := &recording{body: &capped{body: body, limit: o.job.MaxFileBytes}}
	stored, err := o.job.Files.PutStream(ctx, key, source, kept.ContentType)
	switch {
	case errors.Is(err, errTooLarge):
		return o.fail(ctx, document, "The file is larger than the "+sizeText(o.job.MaxFileBytes)+" per-file limit.", err)
	case err != nil && (source.failed != nil || ctx.Err() != nil):
		// The bytes stopped arriving.
		return o.fail(ctx, document, reasonUnavailable, err)
	case err != nil:
		return o.fail(ctx, document, reasonStorage, err)
	case stored.Size == 0:
		o.discard(ctx, key)
		return o.fail(ctx, document, reasonEmpty, errors.New("zero bytes downloaded"))
	}

	// Recorded outside the job's context: the file is stored, and a job
	// that is being stopped must not leave an object no row points at.
	recordCtx, done := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer done()
	wanted, err := o.job.Rows.RecordDownloaded(recordCtx, Downloaded{
		DocumentID: document.ID, UserID: o.userID, ConnectorID: o.connectorID,
		Backend: o.job.Files.Name(), Key: key, Name: kept.Name, ContentType: kept.ContentType,
		Size: stored.Size, SHA256: stored.SHA256, File: file, At: o.job.Now(),
	})
	if err != nil {
		o.discard(ctx, key)
		return o.fail(ctx, document, reasonStorage, err)
	}
	if !wanted {
		// Deleted, or fetched by another job, while this one downloaded.
		o.discard(ctx, key)
		return nil
	}
	// After the file row is committed: the indexer reads it the moment it
	// takes the message. A queue that is down marks the document failed
	// with the file kept, so a re-index needs no download.
	if err := o.job.Indexer.Index(recordCtx, document.ID); err != nil {
		if markErr := o.job.Rows.MarkQueueingFailed(recordCtx, o.userID, document.ID, reasonQueue); markErr != nil {
			return fmt.Errorf("document %d: its index job was not queued (%w) and it could not be marked failed: %w", document.ID, err, markErr)
		}
		return fmt.Errorf("document %d: its index job was not queued: %w", document.ID, err)
	}
	return nil
}

// errTransient marks a failure that may pass by itself: Drive or the store
// did not answer. A sync that met one does not move its place in the change
// feed, so the same change is read, and the file tried, again next time.
var errTransient = errors.New("drivesync: this may work if tried again")

// fail marks the document failed with a reason a person can read, and
// returns the cause for the log.
func (o one) fail(ctx context.Context, document Document, reason string, cause error) error {
	if reason == reasonUnavailable || reason == reasonStorage {
		cause = fmt.Errorf("%w: %w", errTransient, cause)
	}
	markCtx, done := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer done()
	if err := o.job.Rows.MarkFailed(markCtx, o.userID, document.ID, reason); err != nil {
		// Left "downloading"; a later sync finds it abandoned and fetches it.
		return fmt.Errorf("document %d: %w (and it could not be marked failed: %w)", document.ID, cause, err)
	}
	return fmt.Errorf("document %d: %w", document.ID, cause)
}

// discard removes a stored object no row owns.
func (o one) discard(ctx context.Context, key string) {
	cleanCtx, done := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer done()
	if err := o.job.Files.Remove(cleanCtx, key); err != nil {
		o.job.Log.Error("a downloaded file that no document owns could not be removed", "key", key, "error", err.Error())
	}
}

// reasonFor is the sentence a person reads for a failure of Drive's.
func reasonFor(err error) string {
	switch {
	case errors.Is(err, errAuth):
		return reasonAuth
	case errors.Is(err, errGone):
		return reasonGone
	case errors.Is(err, errRefused):
		return reasonRefused
	}
	return reasonUnavailable
}

// sizeText writes a byte limit the way a person reads one.
func sizeText(bytes int64) string {
	const megabyte = 1 << 20
	if bytes >= megabyte && bytes%megabyte == 0 {
		return fmt.Sprintf("%d MB", bytes/megabyte)
	}
	if bytes >= megabyte {
		return fmt.Sprintf("%.1f MB", float64(bytes)/megabyte)
	}
	return fmt.Sprintf("%d bytes", bytes)
}
