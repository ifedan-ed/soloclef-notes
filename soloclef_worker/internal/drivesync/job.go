package drivesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Renewer gets a fresh access token for a stored refresh token
// (drivegrant.Client).
type Renewer interface {
	Refresh(ctx context.Context, refreshToken string) (drivegrant.Grant, error)
}

// Indexer asks the indexing worker to index a document
// (indexrequest.Publisher).
type Indexer interface {
	Index(ctx context.Context, documentID int64) error
}

// Job runs index_google_drive_files messages.
type Job struct {
	Rows    Rows
	Google  Renewer
	Files   filestore.Writer
	Indexer Indexer
	// SecretKey opens and seals the grant's stored tokens: the
	// deployment's secret key, the one the API sealed them with.
	SecretKey string
	// DriveURL is the root of Drive's API.
	DriveURL string
	// HTTP makes the requests to Drive. It follows no redirect (NewHTTP).
	HTTP *http.Client
	// MaxFileBytes is the largest file kept: the upload limit, since an
	// imported file is an upload by another road.
	MaxFileBytes int64
	// Concurrency is how many files one job reads from Drive at once.
	Concurrency int
	Log         *slog.Logger
	// Now is the clock; a test names another.
	Now func() time.Time
}

// NewHTTP is the client Drive is asked with. A reply that redirects is not
// followed: the request carries the account's access token, and a redirect
// must not be able to send it anywhere else. headerTimeout bounds the wait
// for Drive to begin an answer; the whole of a download is bounded by the
// job's deadline.
func NewHTTP(headerTimeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = headerTimeout
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	parsed, err := url.Parse(j.DriveURL)
	switch {
	case j.Rows == nil || j.Google == nil || j.Files == nil || j.Indexer == nil ||
		j.HTTP == nil || j.Log == nil || j.Now == nil:
		return errors.New("drivesync: a dependency is missing")
	case j.SecretKey == "":
		return errors.New("drivesync: no secret key to open stored tokens with")
	case err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http"):
		return fmt.Errorf("drivesync: the Drive API address %q must be http(s):// followed by a host", j.DriveURL)
	case j.MaxFileBytes < 1 || j.Concurrency < 1:
		return errors.New("drivesync: the per-file limit and the concurrency must be at least 1")
	}
	return nil
}

// Run is the jobs.Handler.
func (j *Job) Run(ctx context.Context, msg *celery.Message) error {
	request, err := drivesyncjob.Read(msg.Kwargs)
	if err != nil {
		return err
	}
	connection, err := j.Rows.Connection(ctx, request.UserID, request.ConnectorID)
	if err != nil {
		return err
	}
	if connection == nil {
		// Disconnected since the message was published, or never this
		// account's. Either way there is nothing of theirs to touch.
		j.Log.Info("a Drive sync named a connection the account does not have", "connector", request.ConnectorID)
		return nil
	}
	work := one{job: j, userID: request.UserID, connectorID: connection.ID}
	token, err := j.accessToken(ctx, request.UserID, connection)
	if err != nil {
		// The files just picked are told why they will not arrive; a
		// scheduled sync has nobody waiting.
		reason := reasonUnavailable
		if errors.Is(err, errReconnect) {
			reason = reasonAuth
		}
		work.abandon(ctx, request.FileIDs, reason)
		return fmt.Errorf("drivesync: connection %d: %w", connection.ID, err)
	}
	work.drive = drive{base: j.DriveURL, token: token, http: j.HTTP}

	var done tally
	if len(request.FileIDs) > 0 {
		done, err = work.named(ctx, request.FileIDs)
	} else if connection.Enabled {
		done, err = work.everything(ctx, pageToken(connection.Config))
	}
	j.Log.Info("Drive sync finished", "connector", connection.ID, "named", len(request.FileIDs),
		"fetched", done.fetched, "renamed", done.renamed, "gone", done.gone, "unchanged", done.unchanged, "failed", done.failed)
	return err
}

// pageToken is the feed position kept on the connection, or "".
func pageToken(config []byte) string {
	var held struct {
		Token string `json:"start_page_token"`
	}
	_ = json.Unmarshal(config, &held)
	return held.Token
}

// tally is what one job did, for its one log line.
type tally struct {
	fetched, renamed, gone, unchanged, failed int
}

// results gathers the outcomes of files handled at once.
type results struct {
	mu        sync.Mutex
	tally     tally
	failures  []error
	transient bool
}

func (r *results) add(count func(*tally), err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.tally.failed++
		r.failures = append(r.failures, err)
		r.transient = r.transient || errors.Is(err, errTransient)
		return
	}
	count(&r.tally)
}

// each runs handle for every item, Concurrency at a time, and waits.
func each[T any](ctx context.Context, concurrency int, items []T, handle func(T)) {
	slots := make(chan struct{}, concurrency)
	var running sync.WaitGroup
	for _, item := range items {
		// A job past its deadline starts nothing more; what is in flight
		// ends by its own context.
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		running.Add(1)
		go func() {
			defer running.Done()
			defer func() { <-slots }()
			handle(item)
		}()
	}
	running.Wait()
}

// apply does what decide says for one file Drive has.
func (o one) apply(ctx context.Context, document Document, file remoteFile, out *results) {
	if document.GoneSince != "" {
		// Drive has the file again (restored from the bin, or picked
		// again): the document stops saying it is gone and is synced as
		// before.
		if err := o.job.Rows.MarkBack(ctx, o.userID, document.ID); err != nil {
			out.add(nil, fmt.Errorf("%w: document %d back in Drive: %w", errTransient, document.ID, err))
			return
		}
	}
	switch decide(document, file) {
	case leave:
		out.add(func(t *tally) { t.unchanged++ }, nil)
	case rename:
		err := o.job.Rows.Rename(ctx, o.userID, document.ID, document.Name, file.Name)
		if err != nil {
			err = fmt.Errorf("%w: renaming document %d: %w", errTransient, document.ID, err)
		}
		out.add(func(t *tally) { t.renamed++ }, err)
	case fetch:
		if !document.owed() {
			wanted, err := o.job.Rows.Requeue(ctx, o.userID, document.ID)
			if err != nil {
				out.add(nil, fmt.Errorf("%w: queueing document %d again: %w", errTransient, document.ID, err))
				return
			}
			if !wanted {
				// Deleted between the read and now.
				out.add(func(t *tally) { t.unchanged++ }, nil)
				return
			}
		}
		out.add(func(t *tally) { t.fetched++ }, o.fetch(ctx, document, file))
	}
}

// goneFromDrive is what a file Drive says was binned or removed does to
// its document: nothing to the document itself. The owner's decision: an
// imported file is the notebook's, and Drive losing it must not take it
// away here. The document is marked gone (its metadata's
// google_drive.gone_from_drive_at, which the UI reads to say so) and is
// no longer synced. One still waiting for its download has nothing to
// keep, and is told why its file will not arrive.
func (o one) goneFromDrive(ctx context.Context, document Document, out *results) {
	if document.owed() {
		out.add(nil, o.fail(ctx, document, reasonGone, errGone))
		return
	}
	if document.GoneSince != "" {
		out.add(func(t *tally) { t.unchanged++ }, nil)
		return
	}
	err := o.job.Rows.MarkGone(ctx, o.userID, document.ID, o.job.Now())
	if err != nil {
		err = fmt.Errorf("%w: marking document %d gone from Drive: %w", errTransient, document.ID, err)
	}
	out.add(func(t *tally) { t.gone++ }, err)
}

// check asks Drive about one imported file and does what its answer
// calls for.
func (o one) check(ctx context.Context, document Document, out *results) {
	file, err := o.drive.file(ctx, document.FileID)
	switch {
	case err == nil && !file.Trashed:
		o.apply(ctx, document, file, out)
	case document.owed():
		// Somebody is waiting for this file: they are told why it will
		// not arrive.
		if err == nil {
			err = errGone
		}
		out.add(nil, o.fail(ctx, document, reasonFor(err), err))
	case err == nil:
		// In Drive's bin: gone, as the change feed would have said.
		o.goneFromDrive(ctx, document, out)
	case errors.Is(err, errGone):
		// Left as it is. "Not found" is also what Drive says of a file
		// this grant was never given (the person connected a different
		// Google account since), which says nothing about the file; only
		// Drive saying a file was binned or removed marks it gone.
		out.add(func(t *tally) { t.unchanged++ }, nil)
	case errors.Is(err, errUnavailable):
		out.add(nil, fmt.Errorf("%w: document %d: %w", errTransient, document.ID, err))
	default:
		out.add(nil, fmt.Errorf("document %d: %w", document.ID, err))
	}
}

// named fetches the files a person just picked. Their documents were made
// by the API and are waiting; a file with no document was deleted here
// since, and is left alone.
func (o one) named(ctx context.Context, fileIDs []string) (tally, error) {
	documents, err := o.job.Rows.Documents(ctx, o.userID, fileIDs)
	if err != nil {
		return tally{}, err
	}
	var out results
	each(ctx, o.job.Concurrency, fileIDs, func(id string) {
		if document, imported := documents[id]; imported {
			o.check(ctx, document, &out)
		}
	})
	return out.tally, errors.Join(out.failures...)
}

// abandon tells the waiting documents of named files that their files
// will not arrive, and why.
func (o one) abandon(ctx context.Context, fileIDs []string, reason string) {
	if len(fileIDs) == 0 {
		return
	}
	documents, err := o.job.Rows.Documents(ctx, o.userID, fileIDs)
	if err != nil {
		o.job.Log.Error("the documents of an import that cannot run could not be read", "error", err.Error())
		return
	}
	markCtx, done := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer done()
	for _, document := range documents {
		if !document.owed() {
			continue
		}
		if err := o.job.Rows.MarkFailed(markCtx, o.userID, document.ID, reason); err != nil {
			o.job.Log.Error("a document whose file will not arrive could not be told", "document", document.ID, "error", err.Error())
		}
	}
}

// everything brings all the account imported up to date: from the change
// feed when the connection keeps a position in it, and file by file when
// it does not.
func (o one) everything(ctx context.Context, position string) (tally, error) {
	var out results
	if position != "" {
		next, err := o.fromFeed(ctx, position, &out)
		switch {
		case errors.Is(err, errFeedPosition):
			// Too old for Drive to answer from. Forgotten, so that if the
			// pass below is cut short the next sync starts it again.
			if err := o.job.Rows.DropPageToken(ctx, o.userID, o.connectorID); err != nil {
				return out.tally, err
			}
			position = ""
		case err != nil:
			return out.tally, err
		default:
			position = next
		}
	}
	if position == "" {
		// The position first: whatever changes while the files are being
		// checked is then in the feed the next sync reads.
		now, err := o.drive.startPageToken(ctx)
		if err != nil {
			return out.tally, err
		}
		if err := o.everyFile(ctx, &out); err != nil {
			return out.tally, err
		}
		position = now
	}
	// A file that could not be reached is tried again by reading the same
	// changes again: the position stays where it was.
	if out.transient {
		return out.tally, errors.Join(out.failures...)
	}
	patch, err := json.Marshal(map[string]string{"start_page_token": position})
	if err == nil {
		err = o.job.Rows.MergeConfig(ctx, o.userID, o.connectorID, patch)
	}
	if err == nil {
		err = o.job.Rows.Synced(ctx, o.userID, o.connectorID)
	}
	return out.tally, errors.Join(append(out.failures, err)...)
}

// fromFeed reads every change since a position and acts on the ones that
// are about a file the account imported. It returns the position to keep.
func (o one) fromFeed(ctx context.Context, position string, out *results) (string, error) {
	// The last word on each file: a file changed twice is fetched once,
	// and one changed and then binned is only marked gone.
	latest := map[string]change{}
	var fileIDs []string
	for {
		page, next, last, err := o.drive.changes(ctx, position)
		if err != nil {
			return "", err
		}
		for _, entry := range page {
			if !drivesyncjob.ValidFileID(entry.FileID) {
				continue
			}
			if _, seen := latest[entry.FileID]; !seen {
				fileIDs = append(fileIDs, entry.FileID)
			}
			latest[entry.FileID] = entry
		}
		position = next
		if last {
			break
		}
	}
	// The feed is the whole Drive; the account's documents say which of it
	// was imported. Everything else is passed over without a request.
	documents, err := o.job.Rows.Documents(ctx, o.userID, fileIDs)
	if err != nil {
		return "", err
	}
	// And what the feed cannot know: a document still waiting for a
	// download whose job is gone.
	abandoned, err := o.job.Rows.Abandoned(ctx, o.userID)
	if err != nil {
		return "", err
	}
	work := make([]func(), 0, len(documents)+len(abandoned))
	for _, id := range fileIDs {
		document, imported := documents[id]
		if !imported {
			continue
		}
		entry := latest[id]
		if entry.gone() {
			work = append(work, func() { o.goneFromDrive(ctx, document, out) })
		} else {
			work = append(work, func() { o.apply(ctx, document, *entry.File, out) })
		}
	}
	for _, document := range abandoned {
		if _, changed := documents[document.FileID]; !changed && drivesyncjob.ValidFileID(document.FileID) {
			work = append(work, func() { o.check(ctx, document, out) })
		}
	}
	each(ctx, o.job.Concurrency, work, func(do func()) { do() })
	return position, ctx.Err()
}

// everyFile checks each imported file against Drive, one request a file.
// It is the sync of a connection with no place in the change feed: its
// first, or the one after Drive forgot the place it had.
func (o one) everyFile(ctx context.Context, out *results) error {
	imported, err := o.job.Rows.Imported(ctx, o.userID)
	if err != nil {
		return err
	}
	each(ctx, o.job.Concurrency, imported, func(document Document) {
		// The id is from the document's metadata and goes into a URL.
		if drivesyncjob.ValidFileID(document.FileID) {
			o.check(ctx, document, out)
		}
	})
	return ctx.Err()
}
