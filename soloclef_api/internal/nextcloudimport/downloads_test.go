package nextcloudimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// yard is a download pool over a stand-in Nextcloud, a stand-in database
// and a real file store in a temporary directory.
type yard struct {
	t      testing.TB
	cloud  *cloud
	table  *table
	queue  *queue
	pool   *Downloads
	root   string
	events journal
}

func newYard(t testing.TB, files map[string]node, concurrency int, maxBytes int64) *yard {
	t.Helper()
	y := &yard{t: t, root: t.TempDir()}
	if files == nil {
		files = map[string]node{}
	}
	y.cloud = newCloud(t, files)
	store, err := filestore.OpenLocal(y.root)
	if err != nil {
		t.Fatal(err)
	}
	y.table = &table{documents: map[int64]*held{}, events: &y.events}
	y.queue = &queue{events: &y.events}
	y.pool, err = NewDownloads(context.Background(), concurrency, maxBytes, y.table, store, y.queue,
		func(account Account) nextcloud.Remote {
			return nextcloud.NewClient(account, y.cloud.server.Client().Transport, quick)
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return y
}

// job puts a document waiting for its download in the table and returns
// the job that owes it.
func (y *yard) job(id int64, path string) Job {
	parts := nextcloud.PathParts(path)
	y.table.mu.Lock()
	y.table.documents[id] = &held{owner: owner, imported: Imported{State: "pending", Stage: "downloading", Path: path}}
	y.table.mu.Unlock()
	return Job{DocumentID: id, UserID: owner, Account: y.cloud.account(), Path: path, Name: parts[len(parts)-1], ContentType: "application/pdf"}
}

// finish starts the jobs and waits for the pool to have nothing running.
func (y *yard) finish(jobs ...Job) {
	y.t.Helper()
	y.pool.Start(jobs, produce.LogContext{RequestID: "req-1", UserID: owner})
	finished := make(chan struct{})
	go func() {
		y.pool.running.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		y.t.Fatal("the downloads did not finish")
	}
}

// stored is every object in the file store, by key.
func (y *yard) stored() map[string]string {
	y.t.Helper()
	objects := map[string]string{}
	err := filepath.WalkDir(y.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key, _ := filepath.Rel(y.root, path)
		objects[filepath.ToSlash(key)] = string(content)
		return nil
	})
	if err != nil {
		y.t.Fatal(err)
	}
	return objects
}

func TestADownloadStoresTheFileWritesItsRowThenQueuesTheIndexJob(t *testing.T) {
	y := newYard(t, map[string]node{"/Plans": {folder: true}, "/Plans/a b.PDF": {content: "%PDF the plan"}}, 2, 1<<20)
	y.finish(y.job(101, "/Plans/a b.PDF"))

	document := y.table.document(101)
	// Waiting for the indexer now, not for a download.
	if document.imported.State != "pending" || document.imported.Stage != "" || len(document.files) != 1 {
		t.Fatalf("the document is %+v", document)
	}
	file := document.files[0]
	sum := sha256.Sum256([]byte("%PDF the plan"))
	if file.Backend != "local" || file.Name != "a b.PDF" || file.ContentType != "application/pdf" ||
		file.Size != 13 || file.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("the file row says %+v", file)
	}
	// The key is the uploads' own shape, with the extension in lower case.
	if !strings.HasPrefix(file.Key, "documents/"+owner+"/101/original/") || !strings.HasSuffix(file.Key, ".pdf") {
		t.Errorf("key %q", file.Key)
	}
	if objects := y.stored(); len(objects) != 1 || objects[file.Key] != "%PDF the plan" {
		t.Errorf("the store holds %v", objects)
	}
	// The file row is committed before the job is queued: the worker reads
	// it the moment it takes the message.
	if want := []string{"file row", "queued"}; !reflect.DeepEqual(y.events.list(), want) {
		t.Errorf("order %v, want %v", y.events.list(), want)
	}
	if !reflect.DeepEqual(y.queue.ids(), []int64{101}) {
		t.Errorf("queued %v", y.queue.ids())
	}
}

func TestADownloadThatFailsMarksItsDocumentWithAReasonAPersonCanRead(t *testing.T) {
	for _, test := range []struct {
		name, path string
		before     func(response http.ResponseWriter, request *http.Request) bool
		reason     string
	}{
		{name: "the file is gone", path: "/gone.pdf", reason: "no longer in Nextcloud"},
		{name: "the password was revoked", path: "/a.pdf",
			before: func(response http.ResponseWriter, _ *http.Request) bool {
				response.WriteHeader(http.StatusUnauthorized)
				return true
			}, reason: "Reconnect Nextcloud"},
		{name: "the server is down", path: "/a.pdf",
			before: func(response http.ResponseWriter, _ *http.Request) bool {
				response.WriteHeader(http.StatusInternalServerError)
				return true
			}, reason: "could not be reached"},
		{name: "the connection breaks partway", path: "/a.pdf",
			before: func(response http.ResponseWriter, _ *http.Request) bool {
				response.Header().Set("Content-Length", "1000")
				_, _ = io.WriteString(response, "only this much")
				return true
			}, reason: "stopped sending the file"},
		// Its size was not in the listing, so the plan let it through; the
		// bytes themselves are what is measured.
		{name: "larger than the limit", path: "/big.pdf", reason: "larger than the 1.0 KB per-file limit"},
		{name: "empty", path: "/empty.pdf", reason: "The file is empty."},
	} {
		t.Run(test.name, func(t *testing.T) {
			y := newYard(t, map[string]node{"/a.pdf": {content: "aaaa"}, "/big.pdf": {size: 1025, noSize: true}, "/empty.pdf": {}}, 1, 1<<10)
			y.cloud.before = test.before
			y.finish(y.job(101, test.path))

			document := y.table.document(101)
			if document.imported.State != "failed" || !strings.Contains(document.reason, test.reason) {
				t.Errorf("the document is %s: %q, want failed saying %q", document.imported.State, document.reason, test.reason)
			}
			// The reason is for a person: no address, no status code.
			if strings.Contains(document.reason, "127.0.0.1") || strings.Contains(document.reason, "500") {
				t.Errorf("the reason leaks the cause: %q", document.reason)
			}
			// Nothing of a failed download is kept, and nothing is queued.
			if objects := y.stored(); len(objects) != 0 {
				t.Errorf("the store holds %v", objects)
			}
			if len(y.queue.ids()) != 0 || len(document.files) != 0 {
				t.Error("a failed download wrote a file row or queued a job")
			}
		})
	}
}

func TestAFileOfExactlyTheLimitIsKept(t *testing.T) {
	y := newYard(t, map[string]node{"/edge.pdf": {size: 1 << 10}}, 1, 1<<10)
	y.finish(y.job(101, "/edge.pdf"))
	if document := y.table.document(101); len(document.files) != 1 || document.files[0].Size != 1<<10 {
		t.Errorf("the document is %+v", document)
	}
}

func TestADocumentDeletedWhileItsFileDownloadsKeepsNothing(t *testing.T) {
	y := newYard(t, map[string]node{"/a.pdf": {content: "aaaa"}}, 1, 1<<10)
	job := y.job(101, "/a.pdf")
	y.cloud.before = func(http.ResponseWriter, *http.Request) bool {
		// The delete lands while the bytes are on their way.
		y.table.mu.Lock()
		y.table.documents[101].imported.State = "deleting"
		y.table.mu.Unlock()
		return false
	}
	y.finish(job)
	if objects := y.stored(); len(objects) != 0 {
		t.Errorf("the store holds %v for a document that no longer wants it", objects)
	}
	if document := y.table.document(101); len(document.files) != 0 || document.imported.State != "deleting" || len(y.queue.ids()) != 0 {
		t.Errorf("the document is %+v, queued %v", document, y.queue.ids())
	}
}

func TestOnlySoManyFilesAreFetchedAtOnce(t *testing.T) {
	files := map[string]node{}
	var jobs []Job
	y := newYard(t, files, 2, 1<<10)
	var now, most atomic.Int32
	y.cloud.before = func(http.ResponseWriter, *http.Request) bool {
		running := now.Add(1)
		for {
			seen := most.Load()
			if running <= seen || most.CompareAndSwap(seen, running) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		now.Add(-1)
		return false
	}
	for id := int64(1); id <= 8; id++ {
		path := "/" + itoa(int(id)) + ".pdf"
		y.cloud.mu.Lock()
		y.cloud.files[path] = node{content: "x"}
		y.cloud.mu.Unlock()
		jobs = append(jobs, y.job(id, path))
	}
	y.finish(jobs...)
	if most.Load() != 2 {
		t.Errorf("%d downloads ran at once, want the pool's 2", most.Load())
	}
	if len(y.queue.ids()) != 8 {
		t.Errorf("%d of 8 files were fetched", len(y.queue.ids()))
	}
}

func TestStoppingThePoolFailsWhatItCouldNotFinishAndLeavesNothingBehind(t *testing.T) {
	y := newYard(t, map[string]node{"/slow.pdf": {content: "x"}, "/waiting.pdf": {content: "y"}}, 1, 1<<10)
	reached := make(chan struct{})
	y.cloud.before = func(response http.ResponseWriter, request *http.Request) bool {
		// Half a file, then nothing until the client goes away.
		_, _ = io.WriteString(response, "half")
		response.(http.Flusher).Flush()
		close(reached)
		<-request.Context().Done()
		return true
	}
	// One slot: the first job holds it, the second waits its turn.
	y.pool.Start([]Job{y.job(1, "/slow.pdf")}, produce.LogContext{})
	<-reached
	y.pool.Start([]Job{y.job(2, "/waiting.pdf")}, produce.LogContext{})

	stopping, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := y.pool.Stop(stopping); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		document := y.table.document(id)
		if document.imported.State != "failed" || !strings.Contains(document.reason, "restarted before this file was downloaded") {
			t.Errorf("document %d is %s: %q", id, document.imported.State, document.reason)
		}
	}
	if objects := y.stored(); len(objects) != 0 {
		t.Errorf("the store holds %v", objects)
	}
	if len(y.queue.ids()) != 0 {
		t.Errorf("queued %v", y.queue.ids())
	}
}

func TestAPoolWithAPartMissingIsNotBuilt(t *testing.T) {
	y := newYard(t, nil, 1, 1)
	store, _ := filestore.OpenLocal(t.TempDir())
	connect := func(Account) nextcloud.Remote { return nil }
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for name, build := range map[string]func() (*Downloads, error){
		"no concurrency": func() (*Downloads, error) {
			return NewDownloads(context.Background(), 0, 1, y.table, store, y.queue, connect, logger)
		},
		"no size limit": func() (*Downloads, error) {
			return NewDownloads(context.Background(), 1, 0, y.table, store, y.queue, connect, logger)
		},
		"no queue": func() (*Downloads, error) {
			return NewDownloads(context.Background(), 1, 1, y.table, store, nil, connect, logger)
		},
	} {
		if _, err := build(); err == nil {
			t.Errorf("a pool with %s was built", name)
		}
	}
}

// The standard: a download is never held in memory. 256 MB goes from the
// stand-in Nextcloud through the client into the store while the heap is
// sampled; it may grow by the copy buffers and the HTTP machinery and by
// nothing that scales with the file.
func TestADownloadIsStreamedNotHeld(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 256 MB to the temporary directory")
	}
	const size = 256 << 20
	// Far above what streaming needs and far below the file: holding even
	// one copy of it would be sixteen times over.
	const allowed = 16 << 20
	y := newYard(t, map[string]node{"/large.pdf": {size: size}}, 1, size)
	job := y.job(101, "/large.pdf")

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	stop, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		var now runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&now)
				if now.HeapAlloc > peak.Load() {
					peak.Store(now.HeapAlloc)
				}
			}
		}
	}()
	started := time.Now()
	y.finish(job)
	took := time.Since(started)
	close(stop)
	<-sampled

	document := y.table.document(101)
	if len(document.files) != 1 || document.files[0].Size != size {
		t.Fatalf("the document is %+v: %q", document.imported, document.reason)
	}
	grew := int64(peak.Load()) - int64(before.HeapAlloc)
	t.Logf("256 MB downloaded and stored in %s (%.0f MB/s); the heap peaked %.1f MB above where it began",
		took.Round(time.Millisecond), float64(size>>20)/took.Seconds(), float64(grew)/(1<<20))
	if grew > allowed {
		t.Errorf("the heap grew by %d bytes during a %d byte download; the file is being held, not streamed", grew, size)
	}
}

// BenchmarkDownload is one whole job: the WebDAV GET over loopback, the
// streamed write to a local store with its SHA-256, the file row and the
// queue (both stand-ins). MB/s is the throughput; B/op is what a download
// allocates, which must not grow with the file.
//
// Measured on the development host (AMD EPYC 9645, 20 threads), 2026-11-11,
// with `go test -run xxx -bench Download -benchmem ./internal/nextcloudimport/`:
//
//	1 MB     2.7 ms/op   393 MB/s   46 KB/op    219 allocs/op
//	64 MB    126 ms/op   534 MB/s   74 KB/op   2252 allocs/op
//
// Sixty-four times the bytes cost 28 KB more. TestADownloadIsStreamedNotHeld
// puts the same fact as a limit: a 256 MB download moved the heap by 0.2 MB.
func BenchmarkDownload(b *testing.B) {
	for _, megabytes := range []int64{1, 64} {
		b.Run(itoa(int(megabytes))+"MB", func(b *testing.B) {
			size := megabytes << 20
			y := newYard(b, map[string]node{"/file.pdf": {size: size}}, 1, size)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				y.finish(y.job(int64(iteration+1), "/file.pdf"))
			}
			b.StopTimer()
			if queued := len(y.queue.ids()); queued != b.N {
				b.Fatalf("%d of %d downloads finished", queued, b.N)
			}
		})
	}
}
