package indexing

import (
	"io"
	"sync"
	"time"
)

// Timings is where one job's time went, by step. It is what the log line,
// the metrics and the benchmarks report, so a slow document says which
// service it was waiting on.
type Timings struct {
	// Load is reading the document row.
	Load time.Duration
	// Fetch is time spent reading the file out of storage.
	Fetch time.Duration
	// Parse is the parser's own time, with Fetch taken out.
	Parse time.Duration
	// Vision is the vision model describing pictures, for a document whose
	// upload asked for it.
	Vision time.Duration
	// Chunk is cutting the Markdown.
	Chunk time.Duration
	// Reconcile is reading the existing chunks and asking Qdrant which still
	// have vectors.
	Reconcile time.Duration
	// Embed is the embedding requests.
	Embed time.Duration
	// Vectors is the Qdrant upsert, waited for.
	Vectors time.Duration
	// Write is the Postgres transaction.
	Write time.Duration
	// Cleanup is removing the dropped chunks' vectors.
	Cleanup time.Duration
	// Total is the whole job, including the status updates between steps,
	// so it is a little more than the sum of the others.
	Total time.Duration
}

// fetching is what was learned about reading the file while the parser read
// it.
//
// It is locked because the reads do not all happen on the job's goroutine: a
// parser that uploads the file (the parser service, the hosted ones) hands
// the body to the HTTP transport, which reads it on a goroutine of its own
// and may still be reading when the answer has already arrived.
type fetching struct {
	mu     sync.Mutex
	spent  time.Duration
	bytes  int64
	failed error
}

// fail records why the file could not be opened.
func (f *fetching) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = err
}

// totals is what was read, how long it took, and the open failure if any.
func (f *fetching) totals() (spent time.Duration, bytes int64, failed error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spent, f.bytes, f.failed
}

// timedBody counts the bytes read from storage and the time spent reading
// them, so fetching can be told apart from parsing even though the parser
// does the reading. Two clock reads and an uncontended lock per Read are a
// fraction of a microsecond against reads of 32 KB and more.
type timedBody struct {
	body  io.ReadCloser
	fetch *fetching
}

func (t *timedBody) Read(buffer []byte) (int, error) {
	started := time.Now()
	read, err := t.body.Read(buffer)
	spent := time.Since(started)
	t.fetch.mu.Lock()
	t.fetch.spent += spent
	t.fetch.bytes += int64(read)
	t.fetch.mu.Unlock()
	return read, err
}

func (t *timedBody) Close() error { return t.body.Close() }
