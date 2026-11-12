package rollingdeadline

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// connection stands in for the writer at the bottom of a response chain:
// like net/http's, it can have its deadline moved and can take a reader. It
// records what it was asked, in order, so a test can see that the deadline
// was moved *before* each piece and what shape each piece arrived in.
type connection struct {
	header http.Header
	body   bytes.Buffer
	// events is "deadline", "write <n>" and "readfrom <n>", as they came.
	events []string
	// deadlines is every deadline set.
	deadlines []time.Time
	// handed is what ReadFrom was given each time.
	handed []io.Reader
	// refuse fails every write from the nth event on, with this error.
	refuseAfter int
	refusal     error
}

func (c *connection) Header() http.Header { return c.header }
func (c *connection) WriteHeader(int)     {}

func (c *connection) SetWriteDeadline(deadline time.Time) error {
	c.events = append(c.events, "deadline")
	c.deadlines = append(c.deadlines, deadline)
	return nil
}

func (c *connection) refusing() bool {
	return c.refusal != nil && len(c.events) >= c.refuseAfter
}

func (c *connection) Write(body []byte) (int, error) {
	if c.refusing() {
		return 0, c.refusal
	}
	c.events = append(c.events, "write")
	return c.body.Write(body)
}

func (c *connection) ReadFrom(source io.Reader) (int64, error) {
	if c.refusing() {
		return 0, c.refusal
	}
	c.events = append(c.events, "readfrom")
	c.handed = append(c.handed, source)
	return c.body.ReadFrom(source)
}

// plain is a connection that cannot take a reader, as a writer chain that
// ends in something other than net/http's own writer cannot. The connection
// is a named field and not embedded, or its ReadFrom would be promoted.
type plain struct{ under *connection }

func (p plain) Write(body []byte) (int, error) { return p.under.Write(body) }
func (p plain) Header() http.Header            { return p.under.Header() }
func (p plain) WriteHeader(int)                {}
func (p plain) SetWriteDeadline(deadline time.Time) error {
	return p.under.SetWriteDeadline(deadline)
}

// onDisk is a file of n patterned bytes, open for reading.
func onDisk(t *testing.T, n int) (*os.File, []byte) {
	t.Helper()
	content := make([]byte, n)
	for index := range content {
		content[index] = byte(index*7 + index>>9)
	}
	path := filepath.Join(t.TempDir(), "object")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file, content
}

func wrapped(t *testing.T, response http.ResponseWriter) *Writer {
	t.Helper()
	writer, err := Wrap(response, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return writer
}

// alternates says the events are deadline, <kind>, deadline, <kind>, ... :
// no piece went out under the deadline of the one before it.
func alternates(t *testing.T, events []string, kind string, pieces int) {
	t.Helper()
	if len(events) != 2*pieces {
		t.Fatalf("%d events, want %d pieces each after a deadline: %v", len(events), pieces, events)
	}
	for index, event := range events {
		want := "deadline"
		if index%2 == 1 {
			want = kind
		}
		if event != want {
			t.Fatalf("event %d is %q, want %q: %v", index, event, want, events)
		}
	}
}

// The point of the package: a writer whose deadline cannot be moved is
// refused, not streamed to under whatever fixed deadline it has.
func TestAWriterWhoseDeadlineCannotBeMovedIsRefused(t *testing.T) {
	// httptest's recorder has no connection and so no deadline -- the same
	// answer http.ResponseController gives for a wrapper with no Unwrap.
	_, err := Wrap(httptest.NewRecorder(), time.Minute)
	if !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("got %v, want a refusal that says the deadline is not supported", err)
	}
}

func TestTheDeadlineIsMovedBeforeAnythingIsSent(t *testing.T) {
	bottom := &connection{header: http.Header{}}
	before := time.Now()
	wrapped(t, bottom)
	if len(bottom.deadlines) != 1 {
		t.Fatalf("%d deadlines set by Wrap, want 1", len(bottom.deadlines))
	}
	if ahead := bottom.deadlines[0].Sub(before); ahead < time.Minute || ahead > time.Minute+5*time.Second {
		t.Errorf("the deadline is %v ahead, want the stall timeout", ahead)
	}
}

// A file goes to the writer underneath as readers over the *os.File itself,
// which is the one shape the kernel can send -- and in pieces, each with a
// deadline of its own, because one sendfile call has one deadline.
func TestAFileIsHandedDownInPiecesTheKernelCanSend(t *testing.T) {
	const size = 2*sendfileChunk + 1000
	file, content := onDisk(t, size)
	bottom := &connection{header: http.Header{}}
	writer := wrapped(t, bottom)
	bottom.events = nil // Wrap's own first deadline is not part of the copy

	sent, err := io.Copy(writer, &io.LimitedReader{R: file, N: size})
	if err != nil || sent != size || !bytes.Equal(bottom.body.Bytes(), content) {
		t.Fatalf("sent %d of %d, err %v, intact %v", sent, size, err, bytes.Equal(bottom.body.Bytes(), content))
	}
	alternates(t, bottom.events, "readfrom", 3)
	for index, handed := range bottom.handed {
		limited, isLimited := handed.(*io.LimitedReader)
		if !isLimited {
			t.Fatalf("piece %d arrived as %T, which the kernel cannot send", index, handed)
		}
		if _, isFile := limited.R.(*os.File); !isFile {
			t.Fatalf("piece %d reads from %T, not the file", index, limited.R)
		}
	}
	if writer.ClientError() != nil {
		t.Errorf("a finished stream reports %v", writer.ClientError())
	}
}

// filestore's range reader reads how much is left from the reader it handed
// over, so that count has to be kept true.
func TestABoundedFileIsSentToItsBoundAndTheBoundIsKept(t *testing.T) {
	file, content := onDisk(t, 500_000)
	if _, err := file.Seek(100_000, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	bottom := &connection{header: http.Header{}}
	bounded := &io.LimitedReader{R: file, N: 300_000}

	sent, err := wrapped(t, bottom).ReadFrom(bounded)
	if err != nil || sent != 300_000 || !bytes.Equal(bottom.body.Bytes(), content[100_000:400_000]) {
		t.Fatalf("sent %d, err %v", sent, err)
	}
	if bounded.N != 0 {
		t.Errorf("the caller's reader says %d bytes are left", bounded.N)
	}
}

// A file that is shorter than its bound ends the stream; it is the caller
// who knows what was promised and says so.
func TestAFileShorterThanItsBoundEndsTheStream(t *testing.T) {
	file, content := onDisk(t, 1000)
	bottom := &connection{header: http.Header{}}
	bounded := &io.LimitedReader{R: file, N: 5000}

	sent, err := wrapped(t, bottom).ReadFrom(bounded)
	if err != nil || sent != 1000 || !bytes.Equal(bottom.body.Bytes(), content) {
		t.Fatalf("sent %d, err %v", sent, err)
	}
	if bounded.N != 4000 {
		t.Errorf("the caller's reader says %d bytes are left, want the 4000 that never came", bounded.N)
	}
}

// A whole file handed over as itself is sent to its end.
func TestAWholeFileIsSentToItsEnd(t *testing.T) {
	file, content := onDisk(t, sendfileChunk+10)
	bottom := &connection{header: http.Header{}}

	sent, err := wrapped(t, bottom).ReadFrom(file)
	if err != nil || sent != int64(len(content)) || !bytes.Equal(bottom.body.Bytes(), content) {
		t.Fatalf("sent %d of %d, err %v", sent, len(content), err)
	}
}

// Anything that is not a file -- an object store's network stream -- is
// copied through a buffer, a deadline before every piece.
func TestAStreamThatIsNotAFileIsCopiedInPieces(t *testing.T) {
	content := bytes.Repeat([]byte("soloclef"), (3*copyChunk)/8)
	bottom := &connection{header: http.Header{}}
	writer := wrapped(t, bottom)
	bottom.events = nil

	// iotest-style: a reader with no other method, handing out all it is
	// asked for.
	sent, err := io.Copy(writer, struct{ io.Reader }{bytes.NewReader(content)})
	if err != nil || sent != int64(len(content)) || !bytes.Equal(bottom.body.Bytes(), content) {
		t.Fatalf("sent %d of %d, err %v", sent, len(content), err)
	}
	alternates(t, bottom.events, "write", 3)
}

// A file into a chain that cannot take a reader is still sent, by the same
// buffer.
func TestAFileIntoAWriterThatCannotTakeAReaderIsCopied(t *testing.T) {
	file, content := onDisk(t, copyChunk+500)
	bottom := &connection{header: http.Header{}}
	writer := wrapped(t, plain{bottom})
	bottom.events = nil

	sent, err := writer.ReadFrom(file)
	if err != nil || sent != int64(len(content)) || !bytes.Equal(bottom.body.Bytes(), content) {
		t.Fatalf("sent %d of %d, err %v", sent, len(content), err)
	}
	alternates(t, bottom.events, "write", 2)
}

// One Write of a large body is not one deadline for the lot.
func TestALargeWriteIsPacedToo(t *testing.T) {
	content := bytes.Repeat([]byte{'m'}, 2*copyChunk+1)
	bottom := &connection{header: http.Header{}}
	writer := wrapped(t, bottom)
	bottom.events = nil

	written, err := writer.Write(content)
	if err != nil || written != len(content) || !bytes.Equal(bottom.body.Bytes(), content) {
		t.Fatalf("wrote %d of %d, err %v", written, len(content), err)
	}
	alternates(t, bottom.events, "write", 3)
}

var errDisk = errors.New("input/output error")

// failingAfter reads n bytes and then fails as a disk or a remote store does.
type failingAfter struct{ left int }

func (f *failingAfter) Read(buffer []byte) (int, error) {
	if f.left == 0 {
		return 0, errDisk
	}
	count := min(len(buffer), f.left)
	f.left -= count
	return count, nil
}

// Whose failure it was is what the caller logs by, so each end's is kept
// apart.
func TestWhichEndFailedIsKnown(t *testing.T) {
	t.Run("the source", func(t *testing.T) {
		writer := wrapped(t, &connection{header: http.Header{}})
		sent, err := writer.ReadFrom(&failingAfter{left: 1000})
		if !errors.Is(err, errDisk) || sent != 1000 {
			t.Fatalf("sent %d, err %v", sent, err)
		}
		if writer.ClientError() != nil || writer.Stalled() {
			t.Errorf("a source's failure was laid at the client's door: %v", writer.ClientError())
		}
	})
	t.Run("the client left, copying", func(t *testing.T) {
		gone := errors.New("write: broken pipe")
		writer := wrapped(t, &connection{header: http.Header{}, refusal: gone, refuseAfter: 3})
		_, err := writer.ReadFrom(struct{ io.Reader }{bytes.NewReader(make([]byte, 4*copyChunk))})
		if !errors.Is(err, gone) || !errors.Is(writer.ClientError(), gone) || writer.Stalled() {
			t.Fatalf("err %v, client error %v, stalled %v", err, writer.ClientError(), writer.Stalled())
		}
	})
	t.Run("the client stalled, copying", func(t *testing.T) {
		writer := wrapped(t, &connection{header: http.Header{}, refusal: os.ErrDeadlineExceeded, refuseAfter: 3})
		_, err := writer.ReadFrom(struct{ io.Reader }{bytes.NewReader(make([]byte, 4*copyChunk))})
		if !errors.Is(err, os.ErrDeadlineExceeded) || !writer.Stalled() {
			t.Fatalf("err %v, stalled %v", err, writer.Stalled())
		}
	})
	t.Run("the client stalled, by sendfile", func(t *testing.T) {
		file, _ := onDisk(t, 3*sendfileChunk)
		writer := wrapped(t, &connection{header: http.Header{}, refusal: os.ErrDeadlineExceeded, refuseAfter: 3})
		sent, err := writer.ReadFrom(file)
		if !errors.Is(err, os.ErrDeadlineExceeded) || !writer.Stalled() || sent != sendfileChunk {
			t.Fatalf("sent %d, err %v, stalled %v", sent, err, writer.Stalled())
		}
	})
	t.Run("the disk failed, by sendfile", func(t *testing.T) {
		// One call does the reading and the writing, so the error itself
		// has to say whose it is; one that is not a socket's is the source's.
		file, _ := onDisk(t, 3*sendfileChunk)
		writer := wrapped(t, &connection{header: http.Header{}, refusal: errDisk, refuseAfter: 3})
		_, err := writer.ReadFrom(file)
		if !errors.Is(err, errDisk) || writer.ClientError() != nil {
			t.Fatalf("err %v, client error %v", err, writer.ClientError())
		}
	})
}

// http.ResponseController must still find the connection underneath.
func TestTheWriterUnderneathStaysReachable(t *testing.T) {
	bottom := &connection{header: http.Header{}}
	writer := wrapped(t, bottom)
	if writer.Unwrap() != http.ResponseWriter(bottom) {
		t.Fatal("Unwrap does not return the writer underneath")
	}
	if err := http.NewResponseController(writer).SetWriteDeadline(time.Now()); err != nil {
		t.Fatalf("a deadline set through the writer: %v", err)
	}
}
