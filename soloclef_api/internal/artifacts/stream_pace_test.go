package artifacts

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/gatedanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// These tests are about time, so they run against a real http.Server on a
// real TCP connection: the write timeout is the server's, the socket buffers
// are the kernel's, and the client is a reader the test can slow down, stop
// or hang up. A recorder cannot stand in for any of that.
//
// The server here has a write timeout of half a second where cmd/api's has
// sixty, and every route is given the same half second as its stall timeout,
// which is how cmd/api wires them. Everything else is as it is served: the
// request-id, CORS and analytics wrappers around the Huma router, so the
// deadline and the copy travel the same chain of writers they do in
// production.

// timeout is the server's write timeout and the routes' stall timeout.
const timeout = 500 * time.Millisecond

// A film large enough that no socket buffer swallows it: the server has to
// wait for the client to read.
const filmSize = 32 << 20

// lockedLog is a log the server's goroutines write and the test reads.
type lockedLog struct {
	mutex sync.Mutex
	text  bytes.Buffer
}

func (l *lockedLog) Write(line []byte) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.Write(line)
}

func (l *lockedLog) String() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.String()
}

// wire is the three byte routes on a listening server.
type wire struct {
	address string
	log     *lockedLog
	// answered receives once for each request the server has completely
	// finished with, access line included.
	answered chan struct{}
}

// noEvents is an analytics sink nobody reads.
type noEvents struct{}

func (noEvents) Record(gatedanalytics.Event) {}

// onTheWire serves a studio's files. `store` is what the routes read (the
// studio's own, or a stand-in wrapped around it) and `rows` the file rows.
func onTheWire(t testing.TB, s *studio, store filestore.Store, rows FileRows) *wire {
	t.Helper()
	served := &wire{log: &lockedLog{}, answered: make(chan struct{}, 8)}
	logger := slog.New(slog.NewTextHandler(served.log, nil))
	gate := Gate{Errors: errorenvelope.Writer{ReportURL: origin + "/contact"}, Logger: logger}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	stores := setOf(t, store)
	DownloadRoute{Gate: gate, Downloads: s, Files: stores, StallTimeout: timeout}.Register(api)
	FileContentRoute{Gate: gate, Rows: rows, Files: stores, StallTimeout: timeout}.Register(api)

	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: callerID, Method: principal.MethodSession})))
	})
	measured := gatedanalytics.Middleware{
		Sink:     noEvents{},
		Template: gatedanalytics.RouteTemplates(http.MethodGet + " " + FileContentPath),
	}.Wrap(withCaller)
	chain := requestcontext.Middleware(logger, corsmirror.NewPolicy([]string{origin}).Middleware(measured))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			chain.ServeHTTP(response, request)
			served.answered <- struct{}{}
		}),
		WriteTimeout: timeout,
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	served.address = listener.Addr().String()
	return served
}

// finished waits for the server to be done with a request.
func (w *wire) finished(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-w.answered:
	case <-time.After(within):
		t.Fatalf("the server was still holding the request after %v; its log: %s", within, w.log)
	}
}

// viewer is a client whose reading the test controls.
type viewer struct {
	connection net.Conn
	response   *http.Response
}

// watch asks for a path and reads the answer's headers, no further.
func (w *wire) watch(t *testing.T, path string, headers ...string) *viewer {
	t.Helper()
	connection, err := net.Dial("tcp", w.address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	// A small receive buffer, so that a viewer that stops reading is felt by
	// the server after kilobytes rather than megabytes.
	connection.(*net.TCPConn).SetReadBuffer(64 << 10)
	request := "GET " + path + " HTTP/1.1\r\nHost: studio\r\n"
	for _, header := range headers {
		request += header + "\r\n"
	}
	if _, err := io.WriteString(connection, request+"\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReaderSize(connection, 64<<10), nil)
	if err != nil {
		t.Fatal(err)
	}
	return &viewer{connection: connection, response: response}
}

// take reads up to n bytes of the body, pausing between megabytes, and
// reports how many came and the digest of them.
func (v *viewer) take(n int64, pause time.Duration, digest io.Writer) (int64, error) {
	var taken int64
	megabyte := make([]byte, 1<<20)
	for taken < n {
		count, err := io.ReadFull(v.response.Body, megabyte[:min(int64(len(megabyte)), n-taken)])
		taken += int64(count)
		if digest != nil {
			digest.Write(megabyte[:count])
		}
		if err != nil {
			return taken, err
		}
		time.Sleep(pause)
	}
	return taken, nil
}

// filmStudio is a studio holding one large file, row 88.
func filmStudio(t testing.TB) (*studio, []byte) {
	t.Helper()
	s := newStudio(t)
	content := bytesOf(filmSize)
	s.put(t, videoStorageKey, content)
	s.file = &StoredFile{ID: 88, Role: "primary", Filename: "film.mp4", MimeType: "video/mp4",
		Size: filmSize, Backend: "local", Key: videoStorageKey, Checksum: inventedChecksum}
	s.download = &Download{Title: "Film", File: s.file}
	return s, content
}

// notADisk is a store whose streams are only streams, as an object store's
// are: the file underneath cannot be seen, so nothing can be handed to the
// kernel and every byte is copied through this process.
type notADisk struct{ filestore.Store }

type onlyAStream struct {
	io.Reader
	io.Closer
}

func (n notADisk) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	stream, err := n.Store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return onlyAStream{stream, stream}, nil
}

func (n notADisk) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	stream, err := n.Store.OpenRange(ctx, key, offset, length)
	if err != nil {
		return nil, err
	}
	return onlyAStream{stream, stream}, nil
}

// The defect: the server's write timeout bounded the whole response, so a
// stream that took longer than it was cut mid-body, after a status and a
// Content-Length that promised all of it. Here the viewer takes four times
// the write timeout to read the film, and gets the film.
func TestAStreamSlowerThanTheWriteTimeoutArrivesWhole(t *testing.T) {
	s, content := filmStudio(t)
	want := sha256.Sum256(content)
	for name, test := range map[string]struct {
		store   filestore.Store
		path    string
		headers []string
		status  int
	}{
		"a player's opening range, off a disk":  {s.files, fileURL, []string{"Range: bytes=0-"}, 206},
		"the whole file, off a disk":            {s.files, fileURL, nil, 200},
		"the download button":                   {s.files, downloadURL, nil, 200},
		"a range from a store that is no disk":  {notADisk{s.files}, fileURL, []string{"Range: bytes=0-"}, 206},
		"the whole of one that is no disk":      {notADisk{s.files}, fileURL, nil, 200},
		"a seek into the middle of the stream":  {s.files, fileURL, []string{fmt.Sprintf("Range: bytes=%d-", filmSize/4)}, 206},
		"a seek into one that is no disk":       {notADisk{s.files}, fileURL, []string{fmt.Sprintf("Range: bytes=%d-", filmSize/4)}, 206},
		"a document's worth of Markdown":        {s.files, downloadURL, nil, 200},
		"a download from a store that is none":  {notADisk{s.files}, downloadURL, nil, 200},
		"a range that ends before the file's":   {s.files, fileURL, []string{fmt.Sprintf("Range: bytes=0-%d", filmSize-2)}, 206},
		"an If-Range that still holds, resumed": {s.files, fileURL, []string{"Range: bytes=0-", `If-Range: "` + inventedChecksum + `"`}, 206},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			studio := *s
			expected, digestWanted := int64(filmSize), want
			switch {
			case strings.Contains(name, "Markdown"):
				// An artifact with no file is its document, sent from memory.
				text := strings.Repeat("A line of a long document.\n", filmSize/27)
				studio.download = &Download{Title: "Notes", Markdown: text}
				expected, digestWanted = int64(len(text)), sha256.Sum256([]byte(text))
			case strings.Contains(name, "seek"):
				expected, digestWanted = filmSize-filmSize/4, sha256.Sum256(content[filmSize/4:])
			case strings.Contains(name, "ends before"):
				expected, digestWanted = filmSize-1, sha256.Sum256(content[:filmSize-1])
			}
			served := onTheWire(t, &studio, test.store, &studio)
			viewer := served.watch(t, test.path, test.headers...)
			if viewer.response.StatusCode != test.status || viewer.response.ContentLength != expected {
				t.Fatalf("status %d, Content-Length %d", viewer.response.StatusCode, viewer.response.ContentLength)
			}

			started := time.Now()
			digest := sha256.New()
			// A megabyte, then a pause: about two seconds for the film,
			// against a write timeout of half a second.
			taken, err := viewer.take(expected, 60*time.Millisecond, digest)
			took := time.Since(started)

			if err != nil || taken != expected {
				t.Fatalf("%d of %d bytes arrived in %v, then: %v\nserver log: %s", taken, expected, took, err, served.log)
			}
			if !bytes.Equal(digest.Sum(nil), digestWanted[:]) {
				t.Error("the bytes that arrived are not the file's")
			}
			if took < 2*timeout {
				t.Fatalf("the stream took %v, which does not outlast the %v write timeout: the test proves nothing", took, timeout)
			}
			served.finished(t, 5*time.Second)
			if log := served.log.String(); strings.Contains(log, "level=WARN") || strings.Contains(log, "level=ERROR") {
				t.Errorf("a stream that arrived whole was logged as a problem: %s", log)
			}
		})
	}
}

// The other half: the timeout still does the job it is there for. A viewer
// that stops reading altogether is dropped once the stall timeout has passed,
// while it is still not reading -- and the server says that it did so.
func TestAViewerThatStopsReadingIsDropped(t *testing.T) {
	for name, store := range map[string]func(filestore.Store) filestore.Store{
		"off a disk":                   func(store filestore.Store) filestore.Store { return store },
		"from a store that is no disk": func(store filestore.Store) filestore.Store { return notADisk{store} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, _ := filmStudio(t)
			served := onTheWire(t, s, store(s.files), s)
			viewer := served.watch(t, fileURL, "Range: bytes=0-")
			if taken, err := viewer.take(1<<20, 0, nil); err != nil || taken != 1<<20 {
				t.Fatalf("the first megabyte: %d bytes, %v", taken, err)
			}

			// The viewer now reads nothing. The server must give up on its
			// own, well before the viewer does anything else.
			stopped := time.Now()
			served.finished(t, 10*timeout)
			if held := time.Since(stopped); held < timeout/2 {
				t.Errorf("dropped after %v, before the stall timeout of %v could have passed", held, timeout)
			}

			rest, err := viewer.take(filmSize, 0, nil)
			if err == nil || 1<<20+rest >= filmSize {
				t.Fatalf("a stalled viewer received the whole film (%d more bytes, err %v)", rest, err)
			}
			log := served.log.String()
			if !strings.Contains(log, "level=WARN") || !strings.Contains(log, "stopped reading a stream was dropped") {
				t.Errorf("the drop was not logged as a warning: %s", log)
			}
			if strings.Contains(log, "level=ERROR") {
				t.Errorf("a stalled viewer is not a fault of the server's: %s", log)
			}
		})
	}
}

// A viewer that hangs up mid-stream -- a seek, a closed tab -- is not a
// problem and is not logged as one.
func TestAViewerLeavingMidStreamIsNotAProblem(t *testing.T) {
	s, _ := filmStudio(t)
	served := onTheWire(t, s, s.files, s)
	viewer := served.watch(t, fileURL, "Range: bytes=0-")
	if _, err := viewer.take(1<<20, 0, nil); err != nil {
		t.Fatal(err)
	}
	viewer.connection.Close()
	served.finished(t, 5*time.Second)

	log := served.log.String()
	if !strings.Contains(log, `level=INFO msg="the client left mid-stream"`) {
		t.Errorf("not noted: %s", log)
	}
	if strings.Contains(log, "level=WARN") || strings.Contains(log, "level=ERROR") {
		t.Errorf("logged as a problem: %s", log)
	}
}

// breaksAfter is a store whose streams give out part-way: with an error, as a
// failing disk or a dropped connection to an object store does, or without
// one, as an object that is shorter than its recorded size does.
type breaksAfter struct {
	filestore.Store
	bytes int64
	with  error
}

type brokenStream struct {
	io.ReadCloser
	left int64
	with error
}

func (b *brokenStream) Read(buffer []byte) (int, error) {
	if b.left == 0 {
		if b.with == nil {
			return 0, io.EOF
		}
		return 0, b.with
	}
	count, err := b.ReadCloser.Read(buffer[:min(int64(len(buffer)), b.left)])
	b.left -= int64(count)
	return count, err
}

func (b breaksAfter) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	stream, err := b.Store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return &brokenStream{ReadCloser: stream, left: b.bytes, with: b.with}, nil
}

// A stream the *server* could not finish is a fault, and used to be logged
// at INFO among the closed tabs.
func TestAStoreThatFailsMidStreamIsAnError(t *testing.T) {
	for name, test := range map[string]struct {
		with    error
		message string
	}{
		"with an error":   {errors.New("read: input/output error"), "a stored file failed mid-stream"},
		"by ending short": {nil, "a stored file ended before its promised length"},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := filmStudio(t)
			served := onTheWire(t, s, breaksAfter{Store: s.files, bytes: 3 << 20, with: test.with}, s)
			viewer := served.watch(t, fileURL)

			taken, err := viewer.take(filmSize, 0, nil)
			if err == nil || taken != 3<<20 {
				t.Fatalf("%d bytes arrived, then: %v", taken, err)
			}
			served.finished(t, 5*time.Second)
			log := served.log.String()
			if !strings.Contains(log, `level=ERROR msg="`+test.message+`"`) ||
				!strings.Contains(log, "bytes_sent=3145728") || !strings.Contains(log, "bytes_promised=33554432") ||
				!strings.Contains(log, "storage_key="+videoStorageKey) {
				t.Errorf("not logged as the server's fault, with what would find it: %s", log)
			}
		})
	}
}

// waitsForTheCaller is a database that answers only once the caller has gone,
// as pgx does when a request's context is cancelled mid-query.
type waitsForTheCaller struct{}

func (waitsForTheCaller) FileIn(ctx context.Context, _ string, _, _ int64) (*StoredFile, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A viewer that abandons a request before it is answered -- every seek of a
// video, every panel closed while loading -- used to be logged at ERROR as a
// failed route, and "answered" with a 500 nobody was there to read.
func TestAViewerLeavingBeforeTheAnswerIsNotAnError(t *testing.T) {
	s, _ := videoStudio(t)
	served := onTheWire(t, s, s.files, waitsForTheCaller{})
	connection, err := net.Dial("tcp", served.address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "GET "+fileURL+" HTTP/1.1\r\nHost: studio\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // the request is now waiting on its row
	connection.Close()
	served.finished(t, 5*time.Second)

	log := served.log.String()
	if strings.Contains(log, "level=ERROR") || strings.Contains(log, "level=WARN") {
		t.Errorf("a viewer leaving was logged as a failure: %s", log)
	}
	if !strings.Contains(log, `level=INFO msg="the client left before an artifact route answered"`) {
		t.Errorf("not noted: %s", log)
	}
	// The access line says what happened too, not "500".
	if !strings.Contains(log, "status=499") {
		t.Errorf("the access line: %s", log)
	}
}

// A real failure while the caller is still there is still an error and still
// answered.
func TestARouteThatFailsWithItsCallerWaitingIsStillAnError(t *testing.T) {
	s, _ := videoStudio(t)
	s.databaseDown = true
	got := s.get(t, fileURL, nil)
	if got.status != 500 || !strings.Contains(s.log.String(), `level=ERROR msg="an artifact route failed"`) {
		t.Errorf("status %d, log: %s", got.status, s.log)
	}
}

// A response whose deadline cannot be moved is refused out loud. Streaming
// it under the server's fixed timeout instead is the defect at the top of
// this file, and would come back unnoticed with the next response wrapper
// that forgets `Unwrap`.
func TestAStreamThatCannotBePacedIsRefused(t *testing.T) {
	s, _ := videoStudio(t)
	logger := slog.New(slog.NewTextHandler(s.log, nil))
	gate := Gate{Errors: errorenvelope.Writer{ReportURL: origin + "/contact"}, Logger: logger}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	FileContentRoute{Gate: gate, Rows: s, Files: s.stores, StallTimeout: timeout}.Register(api)

	// A recorder has no connection, and so no deadline to move.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, fileURL, nil)
	mux.ServeHTTP(recorder, request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: callerID, Method: principal.MethodSession})))

	if recorder.Code != 500 || recorder.Body.Len() > 1000 {
		t.Fatalf("status %d with %d bytes", recorder.Code, recorder.Body.Len())
	}
	if log := s.log.String(); !strings.Contains(log, "level=ERROR") || !strings.Contains(log, "could not pace a stream") {
		t.Errorf("log: %s", log)
	}
}

// A byte route nobody gave a stall timeout stops the server from starting.
func TestAByteRouteWithoutAStallTimeoutDoesNotStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registered with no stall timeout")
		}
	}()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0.0.1"))
	FileContentRoute{}.Register(api)
}
