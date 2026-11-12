package nextcloudimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploads"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Indexing queues a document's index job, and marks the document failed if
// it cannot (indexqueue.Indexing).
type Indexing interface {
	Enqueue(ctx context.Context, documentID int64, logContext produce.LogContext) error
}

// Connect makes the client for an account. cmd/api gives one that builds a
// Client over the process's transport; a test gives a stand-in.
type Connect func(Account) nextcloud.Remote

// errTooLarge is what a capped download's reader returns once the file has
// passed the limit.
var errTooLarge = errors.New("nextcloudimport: the file is larger than the limit")

// capped reads at most limit bytes of a download and then fails. The size
// Nextcloud lists is checked when the import is planned; this is the check
// on the bytes themselves, for a file whose size was not listed or has
// grown since.
type capped struct {
	body  io.Reader
	limit int64
	read  int64
}

func (c *capped) Read(buffer []byte) (int, error) {
	if c.read > c.limit {
		return 0, errTooLarge
	}
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

// markTimeout bounds the statements that record a download's end. They run
// outside the pool's context (a pool that is stopping is exactly when a
// document must still be told), so they need a limit of their own.
const markTimeout = 15 * time.Second

// Downloads fetches the files imports owe, a bounded number at a time.
//
// It is a pool in this process and not a queue task because the indexing
// worker reads files from the file store and from nowhere else: something
// has to put the file there first, and this tier already holds the
// connection's credentials and the store's writer. What a queue would have
// given, a job that survives the process, is covered in two halves: Stop
// marks what it could not finish as failed, and a document whose process
// was killed outright is queued again by the next import that names it
// (store.go, "Stuck downloads").
type Downloads struct {
	rows     Rows
	files    filestore.Writer
	indexing Indexing
	connect  Connect
	maxBytes int64
	logger   *slog.Logger

	// ctx is the pool's lifetime; cancelling it ends every download.
	ctx    context.Context
	cancel context.CancelFunc
	// slots is how many downloads may run at once: a politeness limit
	// toward the Nextcloud server as much as a limit on this process.
	slots   chan struct{}
	running sync.WaitGroup
}

// NewDownloads builds the pool. lifetime is the server's own context: when
// it ends, so does every download. concurrency is how many files are
// fetched at once, and maxBytes the largest file kept.
func NewDownloads(lifetime context.Context, concurrency int, maxBytes int64, rows Rows, files filestore.Writer, indexing Indexing, connect Connect, logger *slog.Logger) (*Downloads, error) {
	switch {
	case concurrency < 1:
		return nil, fmt.Errorf("nextcloudimport: the download concurrency must be at least 1, got %d", concurrency)
	case maxBytes < 1:
		return nil, fmt.Errorf("nextcloudimport: the per-file limit must be positive, got %d", maxBytes)
	case rows == nil || files == nil || indexing == nil || connect == nil || logger == nil:
		return nil, errors.New("nextcloudimport: a dependency of the download pool is missing")
	}
	ctx, cancel := context.WithCancel(lifetime)
	return &Downloads{
		rows: rows, files: files, indexing: indexing, connect: connect, maxBytes: maxBytes, logger: logger,
		ctx: ctx, cancel: cancel, slots: make(chan struct{}, concurrency),
	}, nil
}

// Start hands the pool the downloads an import owes and returns at once.
// It is called after the import's transaction has committed.
//
// Each job waits for a slot in a goroutine of its own. A waiting goroutine
// costs a few kilobytes and an import is at most MaxImportFiles of them, so
// the wait needs no second queue to be bounded.
func (d *Downloads) Start(jobs []Job, logContext produce.LogContext) {
	for _, job := range jobs {
		d.running.Add(1)
		go func() {
			defer d.running.Done()
			select {
			case d.slots <- struct{}{}:
				defer func() { <-d.slots }()
				d.run(job, logContext)
			case <-d.ctx.Done():
				// Stopped before its turn came.
				d.fail(job, reasonRestart, d.ctx.Err())
			}
		}()
	}
}

// Stop ends every download and waits for the pool to finish recording
// that, or for ctx to end. Documents whose files were not fetched are
// marked failed with a reason that says to import again; none is left
// looking as if something were still fetching it.
func (d *Downloads) Stop(ctx context.Context) error {
	d.cancel()
	finished := make(chan struct{})
	go func() {
		d.running.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("nextcloudimport: downloads were still being stopped: %w", ctx.Err())
	}
}

// run is one download: Nextcloud to the file store in one stream, then the
// file row, then the index job.
func (d *Downloads) run(job Job, logContext produce.LogContext) {
	started := time.Now()
	body, err := d.connect(job.Account).Open(d.ctx, job.Path)
	if err != nil {
		d.fail(job, d.reasonFor(err), err)
		return
	}
	defer body.Close()

	key, err := uploads.OriginalKey(job.UserID, job.DocumentID, job.Name)
	if err != nil {
		d.fail(job, reasonStorage, err)
		return
	}
	// The download is the store's body: nothing is held between the two,
	// and the store counts and digests the bytes as it writes them. A body
	// that fails partway leaves no object under the key.
	source := &recording{body: &capped{body: body, limit: d.maxBytes}}
	stored, err := d.files.PutStream(d.ctx, key, source, job.ContentType)
	if err != nil {
		switch {
		case errors.Is(err, errTooLarge):
			d.fail(job, "The file is larger than the "+sizeText(d.maxBytes)+" per-file limit.", err)
		case source.failed != nil || d.ctx.Err() != nil:
			// The bytes stopped arriving: the remote side, or this
			// process stopping.
			d.fail(job, d.reasonFor(errors.Join(source.failed, d.ctx.Err())), err)
		default:
			d.fail(job, reasonStorage, err)
		}
		return
	}
	if stored.Size == 0 {
		d.discard(key)
		d.fail(job, "The file is empty.", errors.New("zero bytes downloaded"))
		return
	}

	// Recorded outside the pool's context: the file is stored, and a pool
	// that is stopping must not leave an object no row points at.
	recordCtx, done := context.WithTimeout(context.WithoutCancel(d.ctx), markTimeout)
	defer done()
	wanted, err := d.rows.RecordDownloaded(recordCtx, Downloaded{
		DocumentID: job.DocumentID, UserID: job.UserID, Backend: d.files.Name(), Key: key, Name: job.Name,
		ContentType: job.ContentType, Size: stored.Size, SHA256: stored.SHA256,
	})
	if err != nil {
		d.discard(key)
		d.fail(job, reasonStorage, err)
		return
	}
	if !wanted {
		// Deleted, or queued again and fetched by another job, while this
		// one was downloading. This copy belongs to nothing.
		d.discard(key)
		d.logger.Info("a downloaded Nextcloud file was no longer wanted", "document_id", job.DocumentID)
		return
	}
	d.logger.Info("downloaded from Nextcloud", "document_id", job.DocumentID, "bytes", stored.Size,
		"took", time.Since(started).Round(time.Millisecond).String())

	// After the file row is committed: the worker reads it the moment it
	// takes the message. A queue that is down marks the document failed
	// (indexqueue), with the file kept, so a re-index needs no download.
	_ = d.indexing.Enqueue(recordCtx, job.DocumentID, logContext)
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

// reasonFor is the sentence a person reads for a remote failure.
func (d *Downloads) reasonFor(err error) string {
	switch {
	case d.ctx.Err() != nil:
		return reasonRestart
	case errors.Is(err, nextcloud.ErrAuth):
		return reasonAuth
	case errors.Is(err, nextcloud.ErrNotFound):
		return reasonGone
	}
	return reasonUnavailable
}

// fail marks the document failed and logs why.
func (d *Downloads) fail(job Job, reason string, cause error) {
	d.logger.Error("a Nextcloud file was not imported", "document_id", job.DocumentID, "reason", reason, "error", fmt.Sprint(cause))
	markCtx, done := context.WithTimeout(context.WithoutCancel(d.ctx), markTimeout)
	defer done()
	if err := d.rows.MarkFailed(markCtx, job.DocumentID, job.UserID, reason); err != nil {
		// Left "downloading"; the stale rule queues it again later.
		d.logger.Error("a document whose download failed could not be marked; it is left waiting", "document_id", job.DocumentID, "error", err.Error())
	}
}

// discard removes a stored object no row owns.
func (d *Downloads) discard(key string) {
	cleanCtx, done := context.WithTimeout(context.WithoutCancel(d.ctx), markTimeout)
	defer done()
	if err := d.files.Remove(cleanCtx, key); err != nil {
		d.logger.Error("a downloaded file that no document owns could not be removed", "key", key, "error", err.Error())
	}
}

// sizeText writes a byte limit the way a person reads one. A real limit is
// a whole number of megabytes; the smaller units keep the sentence true
// for a deployment (or a test) that sets a small one.
func sizeText(bytes int64) string {
	const kilobyte, megabyte = 1 << 10, 1 << 20
	switch {
	case bytes >= megabyte && bytes%megabyte == 0:
		return strconv.FormatInt(bytes/megabyte, 10) + " MB"
	case bytes >= megabyte:
		return strconv.FormatFloat(float64(bytes)/megabyte, 'f', 1, 64) + " MB"
	case bytes >= kilobyte:
		return strconv.FormatFloat(float64(bytes)/kilobyte, 'f', 1, 64) + " KB"
	}
	return strconv.FormatInt(bytes, 10) + " bytes"
}
