package objecthttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

func TestParseRange(t *testing.T) {
	const size = 1000
	whole := ByteRange{Kind: Whole, Offset: 0, Length: size}
	unsatisfiable := ByteRange{Kind: Unsatisfiable}
	partial := func(offset, length int64) ByteRange {
		return ByteRange{Kind: Partial, Offset: offset, Length: length}
	}
	for header, want := range map[string]ByteRange{
		// The three forms.
		"bytes=0-0":     partial(0, 1),
		"bytes=0-499":   partial(0, 500),
		"bytes=500-999": partial(500, 500),
		"bytes=500-":    partial(500, 500),
		"bytes=-1":      partial(999, 1),
		"bytes=-300":    partial(700, 300),
		"bytes=999-999": partial(999, 1),
		"bytes=0-":      partial(0, 1000),
		// An end past the object is clamped; a suffix longer than the
		// object is all of it.
		"bytes=900-5000":                     partial(900, 100),
		"bytes=-5000":                        partial(0, 1000),
		"bytes=0-99999999999999999999999999": partial(0, 1000),
		"BYTES=0-9":                          partial(0, 10),
		"bytes= 10 - 19 ":                    partial(10, 10),
		// The one case 416 describes.
		"bytes=1000-":                       unsatisfiable,
		"bytes=1000-2000":                   unsatisfiable,
		"bytes=5000-":                       unsatisfiable,
		"bytes=-0":                          unsatisfiable,
		"bytes=99999999999999999999999999-": unsatisfiable,
		// Not a byte range: ignored, and the whole object is sent.
		"":                whole,
		"bytes":           whole,
		"bytes=":          whole,
		"bytes=abc":       whole,
		"bytes=5":         whole,
		"bytes=-":         whole,
		"bytes=a-b":       whole,
		"bytes=10-5":      whole,
		"bytes=+5-10":     whole,
		"bytes=-5-10":     whole,
		"bytes=5-+10":     whole,
		"bytes=0x10-0x20": whole,
		"bytes=1_0-2_0":   whole,
		"items=0-5":       whole,
		"0-5":             whole,
		// Several ranges: ignored rather than answered with a multipart.
		"bytes=0-9,20-29": whole,
		"bytes=0-9, -5":   whole,
	} {
		if got := ParseRange(header, size); got != want {
			t.Errorf("%q: got %+v, want %+v", header, got, want)
		}
	}
}

func TestParseRangeOfAnEmptyObject(t *testing.T) {
	for header, want := range map[string]ByteRange{
		"bytes=0-":  {Kind: Unsatisfiable},
		"bytes=0-0": {Kind: Unsatisfiable},
		"bytes=-5":  {Kind: Unsatisfiable},
		"bytes=x":   {Kind: Whole},
	} {
		if got := ParseRange(header, 0); got != want {
			t.Errorf("%q: got %+v, want %+v", header, got, want)
		}
	}
}

func TestIfNoneMatch(t *testing.T) {
	const etag = `"abc"`
	for header, want := range map[string]bool{
		`"abc"`:               true,
		`W/"abc"`:             true, // a proxy weakened it
		`"old", "abc"`:        true, // the client holds two versions
		`"old" , W/"abc","x"`: true,
		`*`:                   true,
		`"a,b", "abc"`:        true, // a comma inside a tag is not a separator
		``:                    false,
		`"abd"`:               false,
		`abc`:                 false, // not quoted, so not a tag
		`"ABC"`:               false,
		`"abc`:                false,
		`"old", junk, "abc"`:  false, // nothing after junk is trusted
	} {
		if got := NoneMatch(header, etag); got != want {
			t.Errorf("If-None-Match %s: got %v", header, got)
		}
	}
	if NoneMatch(`*`, "") {
		t.Error("an object with no tag matched")
	}
}

func TestIfRange(t *testing.T) {
	for name, test := range map[string]struct {
		header, etag string
		want         bool
	}{
		"no header: the range stands":         {"", `"abc"`, true},
		"the same tag":                        {`"abc"`, `"abc"`, true},
		"another version":                     {`"old"`, `"abc"`, false},
		"a weak tag never validates a splice": {`W/"abc"`, `"abc"`, false},
		"a weak tag on the object":            {`W/"abc"`, `W/"abc"`, false},
		"a date is not compared":              {"Wed, 04 Mar 2026 05:06:07 GMT", `"abc"`, false},
	} {
		if got := rangeStillValid(test.header, test.etag); got != test.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

const (
	videoKey  = "artifacts/7/31/primary/0123456789abcdef0123456789abcdef.mp4"
	videoSize = 100_000
)

func pattern(n int) []byte {
	content := make([]byte, n)
	for index := range content {
		content[index] = byte(index*7 + index/251)
	}
	return content
}

func storeWith(t testing.TB, objects map[string][]byte) (*filestore.Local, string) {
	t.Helper()
	directory := t.TempDir()
	for key, content := range objects {
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, directory
}

// server answers every request with the one object, over a real socket, so
// that Content-Length and the body are checked by net/http's own client the
// way a browser would check them.
func server(t testing.TB, store filestore.Store, key string, headers Headers) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		answer, err := Prepare(request.Context(), store, key, Request{
			Method:      request.Method,
			Range:       request.Header.Get("Range"),
			IfNoneMatch: request.Header.Get("If-None-Match"),
			IfRange:     request.Header.Get("If-Range"),
		}, headers)
		if err != nil {
			http.Error(response, err.Error(), http.StatusTeapot)
			return
		}
		answer.Send(response)
	}))
	t.Cleanup(srv.Close)
	return srv
}

var videoHeaders = Headers{
	ContentType:  "video/mp4",
	ETag:         `"sha-of-the-video"`,
	CacheControl: "private, max-age=31536000, immutable",
	Disposition:  `inline; filename="clip.mp4"`,
}

func TestAnObjectIsServedTheWayAPlayerNeedsIt(t *testing.T) {
	content := pattern(videoSize)
	store, _ := storeWith(t, map[string][]byte{videoKey: content})
	srv := server(t, store, videoKey, videoHeaders)

	for name, test := range map[string]struct {
		method  string
		request http.Header
		status  int
		// body is what must arrive; nil means none.
		body         []byte
		contentRange string
		// bare marks an answer that describes no representation: no type,
		// no disposition.
		bare bool
	}{
		"the whole object": {
			status: 200, body: content,
		},
		"a HEAD is the headers of the GET and no body": {
			method: http.MethodHead, status: 200,
		},
		"the first kilobyte": {
			request: http.Header{"Range": {"bytes=0-1023"}},
			status:  206, body: content[:1024], contentRange: "bytes 0-1023/100000",
		},
		"from a position to the end, as a seek does": {
			request: http.Header{"Range": {"bytes=90000-"}},
			status:  206, body: content[90000:], contentRange: "bytes 90000-99999/100000",
		},
		"the last hundred bytes": {
			request: http.Header{"Range": {"bytes=-100"}},
			status:  206, body: content[99900:], contentRange: "bytes 99900-99999/100000",
		},
		"an end past the object is clamped": {
			request: http.Header{"Range": {"bytes=99990-200000"}},
			status:  206, body: content[99990:], contentRange: "bytes 99990-99999/100000",
		},
		"the probe a media element opens with": {
			request: http.Header{"Range": {"bytes=0-"}},
			status:  206, body: content, contentRange: "bytes 0-99999/100000",
		},
		"a HEAD with a range": {
			method: http.MethodHead, request: http.Header{"Range": {"bytes=0-9"}},
			status: 206, contentRange: "bytes 0-9/100000",
		},
		"a range past the end": {
			request: http.Header{"Range": {"bytes=100000-"}},
			status:  416, contentRange: "bytes */100000", bare: true,
		},
		"a range that is not one is ignored, not refused": {
			request: http.Header{"Range": {"bytes=later"}},
			status:  200, body: content,
		},
		"several ranges are answered with everything": {
			request: http.Header{"Range": {"bytes=0-9,50-59"}},
			status:  200, body: content,
		},
		"the client already has it": {
			request: http.Header{"If-None-Match": {`"sha-of-the-video"`}},
			status:  304, bare: true,
		},
		"the client has it, and a proxy weakened the tag": {
			request: http.Header{"If-None-Match": {`W/"sha-of-the-video"`}},
			status:  304, bare: true,
		},
		"the client has an older one": {
			request: http.Header{"If-None-Match": {`"older"`}},
			status:  200, body: content,
		},
		"a 304 wins over a range": {
			request: http.Header{"If-None-Match": {`"sha-of-the-video"`}, "Range": {"bytes=0-9"}},
			status:  304, bare: true,
		},
		"resuming the version the client has part of": {
			request: http.Header{"Range": {"bytes=50000-"}, "If-Range": {`"sha-of-the-video"`}},
			status:  206, body: content[50000:], contentRange: "bytes 50000-99999/100000",
		},
		"resuming a version that has since changed sends all of it": {
			request: http.Header{"Range": {"bytes=50000-"}, "If-Range": {`"older"`}},
			status:  200, body: content,
		},
	} {
		t.Run(name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			request, err := http.NewRequest(method, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			for header, values := range test.request {
				request.Header[header] = values
			}
			response, err := srv.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("the body did not arrive whole: %v", err)
			}

			if response.StatusCode != test.status {
				t.Fatalf("status %d, want %d", response.StatusCode, test.status)
			}
			if !bytes.Equal(body, test.body) {
				t.Errorf("%d bytes arrived, want %d", len(body), len(test.body))
			}
			if got := response.Header.Get("Content-Range"); got != test.contentRange {
				t.Errorf("Content-Range %q, want %q", got, test.contentRange)
			}
			// On every answer, whatever its status.
			for header, want := range map[string]string{
				"ETag":                   `"sha-of-the-video"`,
				"Accept-Ranges":          "bytes",
				"X-Content-Type-Options": "nosniff",
			} {
				if got := response.Header.Get(header); got != want {
					t.Errorf("%s %q, want %q", header, got, want)
				}
			}
			if test.bare {
				if got := response.Header.Get("Content-Disposition"); got != "" {
					t.Errorf("a %d carries a disposition: %q", test.status, got)
				}
				return
			}
			// A 200 or a 206: the length is that of what is sent, and a HEAD
			// states the length its GET would have.
			wantLength := len(test.body)
			if method == http.MethodHead {
				wantLength = videoSize
				if test.status == 206 {
					wantLength = 10
				}
			}
			if got := response.Header.Get("Content-Length"); got != strconv.Itoa(wantLength) {
				t.Errorf("Content-Length %q, want %d", got, wantLength)
			}
			for header, want := range map[string]string{
				"Content-Type":        "video/mp4",
				"Content-Disposition": `inline; filename="clip.mp4"`,
				"Cache-Control":       "private, max-age=31536000, immutable",
			} {
				if got := response.Header.Get(header); got != want {
					t.Errorf("%s %q, want %q", header, got, want)
				}
			}
		})
	}
}

// "Immutable for a year" must not ride along on an error.
func TestARefusedRangeIsNotCacheable(t *testing.T) {
	store, _ := storeWith(t, map[string][]byte{videoKey: pattern(videoSize)})
	answer, err := Prepare(context.Background(), store, videoKey,
		Request{Method: http.MethodGet, Range: "bytes=999999-"}, videoHeaders)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	answer.Send(recorder)
	if recorder.Code != 416 || recorder.Header().Get("Cache-Control") != "" {
		t.Errorf("status %d, Cache-Control %q", recorder.Code, recorder.Header().Get("Cache-Control"))
	}
}

// With no checksum to hand, the tag is made from what the store knows, and
// changes when the object does.
func TestATagIsDerivedWhenTheRouteHasNone(t *testing.T) {
	store, directory := storeWith(t, map[string][]byte{videoKey: pattern(4096)})
	path := filepath.Join(directory, filepath.FromSlash(videoKey))
	tagNow := func() string {
		answer, err := Prepare(context.Background(), store, videoKey,
			Request{Method: http.MethodHead}, Headers{ContentType: "audio/mpeg"})
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		answer.Send(recorder)
		return recorder.Header().Get("ETag")
	}

	first := tagNow()
	if len(first) < 3 || first[0] != '"' || first[len(first)-1] != '"' {
		t.Fatalf("not a quoted tag: %s", first)
	}
	if again := tagNow(); again != first {
		t.Errorf("the tag of an unchanged object moved: %s then %s", first, again)
	}
	later := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if rewritten := tagNow(); rewritten == first {
		t.Error("the tag did not change when the object was rewritten")
	}
	// And it round-trips: the derived tag earns a 304.
	answer, err := Prepare(context.Background(), store, videoKey,
		Request{Method: http.MethodGet, IfNoneMatch: tagNow()}, Headers{})
	if err != nil || answer.Status() != http.StatusNotModified {
		t.Errorf("status %v, %v", answer, err)
	}
}

// Everything the client should be told about fails before a byte is written.
func TestAMissingObjectFailsBeforeAnythingIsSent(t *testing.T) {
	store, _ := storeWith(t, map[string][]byte{videoKey: pattern(10)})
	for name, test := range map[string]struct {
		key  string
		want error
	}{
		"gone":      {"artifacts/7/31/primary/ffffffffffffffffffffffffffffffff.mp4", filestore.ErrNotFound},
		"not a key": {"../../etc/passwd", filestore.ErrInvalidKey},
	} {
		t.Run(name, func(t *testing.T) {
			answer, err := Prepare(context.Background(), store, test.key, Request{Method: http.MethodGet}, videoHeaders)
			if answer != nil || !errors.Is(err, test.want) {
				t.Errorf("got %v, %v", answer, err)
			}
		})
	}
}

// An answer that is prepared and then not sent gives its file back.
func TestCloseReleasesAnUnsentAnswer(t *testing.T) {
	store, _ := storeWith(t, map[string][]byte{videoKey: pattern(10)})
	answer, err := Prepare(context.Background(), store, videoKey, Request{Method: http.MethodGet}, videoHeaders)
	if err != nil {
		t.Fatal(err)
	}
	if err := answer.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := answer.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

// handedOver is a response writer that offers ReadFrom, as net/http's own and
// every writer in cmd/api's chain do, and notes what it was handed.
type handedOver struct {
	*httptest.ResponseRecorder
	sources []io.Reader
}

func (h *handedOver) ReadFrom(source io.Reader) (int64, error) {
	h.sources = append(h.sources, source)
	return io.Copy(h.ResponseRecorder, source)
}

// A whole file off the disk reaches the destination's ReadFrom as the
// *os.File itself, once: that is the shape the kernel can send. io.Copy
// alone never delivers it -- it asks the file for WriteTo first, and os then
// hands the destination a wrapper with the file hidden inside -- which is why
// whole files used to be copied through a buffer while ranges were not.
func TestAWholeFileIsHandedToTheDestinationAsAFile(t *testing.T) {
	content := pattern(300_000)
	store, _ := storeWith(t, map[string][]byte{videoKey: content})
	answer, err := Prepare(context.Background(), store, videoKey, Request{Method: http.MethodGet}, videoHeaders)
	if err != nil {
		t.Fatal(err)
	}
	destination := &handedOver{ResponseRecorder: httptest.NewRecorder()}
	sent, err := answer.Send(destination)
	if err != nil || sent != int64(len(content)) {
		t.Fatalf("sent %d, %v", sent, err)
	}
	if len(destination.sources) != 1 {
		t.Fatalf("ReadFrom was called %d times, want once", len(destination.sources))
	}
	if _, isFile := destination.sources[0].(*os.File); !isFile {
		t.Errorf("the destination was handed a %T, not the file", destination.sources[0])
	}
	if destination.Code != http.StatusOK || !bytes.Equal(destination.Body.Bytes(), content) {
		t.Errorf("status %d, %d bytes", destination.Code, destination.Body.Len())
	}
	// Sent and closed: the file is given back.
	if err := destination.sources[0].(*os.File).Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the file was left open after Send: %v", err)
	}
}

// A destination with no ReadFrom still gets every byte, by the copy.
func TestAWholeFileIsCopiedToADestinationWithNoReadFrom(t *testing.T) {
	content := pattern(300_000)
	store, _ := storeWith(t, map[string][]byte{videoKey: content})
	answer, err := Prepare(context.Background(), store, videoKey, Request{Method: http.MethodGet}, videoHeaders)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	// plainWriter hides the recorder's own ReadFrom, as a middleware's
	// wrapper that forwards nothing would.
	sent, err := answer.Send(plainWriter{recorder})
	if err != nil || sent != int64(len(content)) || !bytes.Equal(recorder.Body.Bytes(), content) {
		t.Errorf("sent %d of %d, %v", sent, len(content), err)
	}
}

// A HEAD and a 304 have no body to hand anywhere.
func TestAnAnswerWithNoBodyHandsNothingOver(t *testing.T) {
	store, _ := storeWith(t, map[string][]byte{videoKey: pattern(10)})
	answer, err := Prepare(context.Background(), store, videoKey, Request{Method: http.MethodHead}, videoHeaders)
	if err != nil {
		t.Fatal(err)
	}
	destination := &handedOver{ResponseRecorder: httptest.NewRecorder()}
	if sent, err := answer.Send(destination); err != nil || sent != 0 || len(destination.sources) != 0 {
		t.Errorf("sent %d, %v, handed over %d", sent, err, len(destination.sources))
	}
}

// ---------------------------------------------------------------------------
// Benchmarks, over a real loopback socket: the whole path a download takes.
// `go test ./objecthttp/ -bench . -benchmem`

const benchmarkObject = 256 << 20

func benchmarkServer(b *testing.B, wrap func(http.ResponseWriter) http.ResponseWriter) (*httptest.Server, string) {
	b.Helper()
	directory := b.TempDir()
	file, err := os.Create(filepath.Join(directory, "big.bin"))
	if err != nil {
		b.Fatal(err)
	}
	piece := pattern(1 << 20)
	for written := 0; written < benchmarkObject; written += len(piece) {
		if _, err := file.Write(piece); err != nil {
			b.Fatal(err)
		}
	}
	file.Close()
	store, err := filestore.OpenLocal(directory)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })
	srv := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		answer, err := Prepare(request.Context(), store, "big.bin", Request{
			Method: request.Method, Range: request.Header.Get("Range"),
		}, Headers{ContentType: "video/mp4"})
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
		answer.Send(wrap(response))
	}))
	b.Cleanup(srv.Close)
	return srv, srv.URL
}

func fetch(b *testing.B, client *http.Client, url, byteRange string, want int64) {
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response, err := client.Do(request)
	if err != nil {
		b.Fatal(err)
	}
	got, err := io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err != nil || got != want {
		b.Fatalf("%d bytes, %v", got, err)
	}
}

// direct is a handler writing straight to net/http's own response writer,
// where the kernel copies file to socket (sendfile).
func direct(response http.ResponseWriter) http.ResponseWriter { return response }

// wrapped is a response writer behind middleware that does not forward
// ReadFrom, so the bytes go through io.Copy's 32 KB buffer. cmd/api's own
// chain forwards it; this row is what any wrapper that forgets to would cost.
type plainWriter struct{ http.ResponseWriter }

func wrapped(response http.ResponseWriter) http.ResponseWriter { return plainWriter{response} }

func BenchmarkServeWholeObject(b *testing.B) {
	for name, wrap := range map[string]func(http.ResponseWriter) http.ResponseWriter{
		"direct": direct, "behind_middleware": wrapped,
	} {
		b.Run(name, func(b *testing.B) {
			srv, url := benchmarkServer(b, wrap)
			b.SetBytes(benchmarkObject)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				fetch(b, srv.Client(), url, "", benchmarkObject)
			}
		})
	}
}

// One megabyte from 200 MiB in: a seek.
func BenchmarkServeRange(b *testing.B) {
	for name, wrap := range map[string]func(http.ResponseWriter) http.ResponseWriter{
		"direct": direct, "behind_middleware": wrapped,
	} {
		b.Run(name, func(b *testing.B) {
			srv, url := benchmarkServer(b, wrap)
			const length = 1 << 20
			header := "bytes=" + strconv.Itoa(200<<20) + "-" + strconv.Itoa(200<<20+length-1)
			b.SetBytes(length)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				fetch(b, srv.Client(), url, header, length)
			}
		})
	}
}
