// Package rollingdeadline lets one response outlive the server's write
// timeout for as long as its client keeps taking bytes, and not a moment
// longer.
//
// # The problem it solves
//
// cmd/api gives its http.Server one WriteTimeout. net/http turns that into a
// single deadline on the connection, set when the request's headers have been
// read and never moved: it bounds the *whole response*, not a stalled write.
// That is right for a JSON answer and wrong for a file. A 45 MB video on a
// slow phone link takes minutes, so the server cut it at the timeout, after a
// 200 that promised a Content-Length -- and every retry was cut at the same
// place, because the web download button, the podcast player and the phone's
// file cache all ask for the whole body again.
//
// Raising the server's timeout would fix the video and unfix everything else:
// the timeout is also what frees a connection whose client has stopped
// reading, and a client that never reads would then hold a goroutine, a file
// and a socket for however long the new number said.
//
// # What it does instead
//
// The deadline is moved forward each time a piece of the body is about to be
// written: "this piece must be accepted within the stall timeout". A client
// that keeps reading, however slowly, never meets the deadline, because each
// piece gets a fresh one. A client that stops reading stops accepting pieces,
// the piece in flight runs out its deadline, the write fails and the handler
// returns. So what is bounded is the time *without progress*, which is the
// thing a write timeout was always meant to bound.
//
// "However slowly" has a floor, and it is deliberately low: one piece per
// stall timeout. With the pieces below and a 60 second timeout that is 0.5
// KB/s for a copied stream and 4.3 KB/s for a file sent by sendfile -- a
// 45 MB video at that speed would take three hours, which no viewer is
// waiting for.
//
// # Sendfile, and why it is sent in pieces
//
// When the source is a file on disk and the connection is plain TCP,
// net/http can hand the copy to the kernel (sendfile) so the bytes never pass
// through this process. That is one call for the whole file, and a deadline
// applies to a whole call, so an unbroken sendfile would bring the original
// problem straight back. The two conflict, and the rolling deadline wins: the
// file is sent as a series of sendfile calls of sendfileChunk bytes, with the
// deadline moved before each. Nothing is lost by it: measured, the pieces are
// faster than one unbroken call (BenchmarkStreamingAFile in
// internal/artifacts, where the figures are).
package rollingdeadline

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"
)

const (
	// copyChunk is how much of a stream is read and written under one
	// deadline when the bytes pass through this process. It is io.Copy's own
	// buffer size, so the copy is no slower than the one it replaces.
	copyChunk = 32 << 10
	// sendfileChunk is how much of a file one sendfile call is asked to send
	// under one deadline. It sets the slowest client that still counts as
	// "making progress" -- one piece per stall timeout -- and that, not
	// throughput, is what chose it: at 256 KB the floor is 4.3 KB/s under a
	// 60 second timeout, which a phone on a poor connection clears. 1 MB
	// measured about a fifth faster on loopback, a speed no real link
	// reaches, and would have put the floor at 17 KB/s, which a 2G link does
	// not always clear; and every size tried, down to 64 KB, was faster than
	// not dividing the file at all.
	sendfileChunk = 256 << 10
)

// buffers are the copyChunk buffers of the copied path, shared between
// requests so a stream allocates nothing per piece.
var buffers = sync.Pool{New: func() any {
	buffer := make([]byte, copyChunk)
	return &buffer
}}

// Writer is a response whose write deadline moves forward with the body.
//
// It is an http.ResponseWriter, so anything that writes a response can write
// through it. Headers and the status pass straight to the writer underneath;
// only the body is paced.
type Writer struct {
	http.ResponseWriter
	control *http.ResponseController
	stall   time.Duration
	// clientErr is the first failure of the client's side: a write the
	// connection refused. See ClientError.
	clientErr error
}

// Wrap gives a response a rolling deadline, and moves it for the first time.
//
// stall is how long the client may go without accepting a piece before it is
// dropped.
//
// It fails if the writer's deadline cannot be moved -- which means some
// wrapper between the route and the connection does not offer
// `Unwrap() http.ResponseWriter`, so http.ResponseController cannot reach the
// connection through it. That is reported and not worked around: streaming
// under the server's fixed timeout instead is exactly the defect this package
// exists to remove, and it would come back silently. Wrap is called before
// the status is written so that the caller can still answer with an error.
func Wrap(response http.ResponseWriter, stall time.Duration) (*Writer, error) {
	writer := &Writer{
		ResponseWriter: response,
		control:        http.NewResponseController(response),
		stall:          stall,
	}
	if err := writer.roll(); err != nil {
		return nil, fmt.Errorf("rollingdeadline: the response's write deadline cannot be moved: %w", err)
	}
	return writer, nil
}

// roll gives the next piece its full stall timeout.
func (w *Writer) roll() error {
	return w.control.SetWriteDeadline(time.Now().Add(w.stall))
}

// Unwrap keeps the writer underneath reachable to http.ResponseController.
func (w *Writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ClientError is the failure that ended the stream if it was the client's
// side that failed, and nil if nothing failed or the source did.
//
// A copy has two ends and io.Copy's one error does not say which broke. The
// caller needs to know, because they mean opposite things: a source that
// fails mid-stream is a fault of this server, and a client that goes away is
// an ordinary Tuesday. Use Stalled to tell a client that was dropped for
// making no progress from one that left.
func (w *Writer) ClientError() error { return w.clientErr }

// Stalled says the stream ended because the client accepted nothing for the
// whole stall timeout and was dropped.
func (w *Writer) Stalled() bool {
	return errors.Is(w.clientErr, os.ErrDeadlineExceeded)
}

// Write sends body in pieces, each under its own deadline. A caller holding
// several megabytes in memory (a long document) gets the same treatment as a
// stream: one Write of the lot would have one deadline for the lot.
func (w *Writer) Write(body []byte) (int, error) {
	written := 0
	for len(body) > 0 {
		piece := body[:min(len(body), copyChunk)]
		if err := w.roll(); err != nil {
			return written, w.failed(err)
		}
		count, err := w.ResponseWriter.Write(piece)
		written += count
		if err != nil {
			return written, w.failed(err)
		}
		body = body[count:]
	}
	return written, nil
}

// failed records the first failure of the client's side and hands it back.
func (w *Writer) failed(err error) error {
	if w.clientErr == nil {
		w.clientErr = err
	}
	return err
}

// ReadFrom is the whole copy, and is what io.Copy calls when this writer is
// its destination. It picks one of two ways to move the bytes:
//
//   - a file on disk, into a writer that itself offers ReadFrom, goes by
//     sendfile in pieces (see the package comment);
//   - anything else -- an object store's network stream, or a writer chain
//     that cannot do sendfile -- is read into a pooled buffer and written,
//     a piece at a time.
//
// Either way the deadline moves before every piece, and memory use does not
// depend on the size of what is sent.
func (w *Writer) ReadFrom(source io.Reader) (int64, error) {
	if file, limit, bounded := fileOf(source); file != nil {
		if kernel, offered := w.ResponseWriter.(io.ReaderFrom); offered {
			sent, err := w.sendFile(kernel, file, limit)
			if bounded != nil {
				// The caller reads how much is left from its own reader
				// (filestore's range reader does), so it is kept true.
				bounded.N -= sent
			}
			return sent, err
		}
	}
	return w.copy(source)
}

// fileOf recognises the two shapes of "a file on disk" that the kernel can
// send: an *os.File (all of it, from where it is), and an *io.LimitedReader
// over one (that many bytes of it). These are the shapes net's sendfile
// accepts and the two that internal/filestore hands out. For a file the
// limit is "until it ends".
func fileOf(source io.Reader) (file *os.File, limit int64, bounded *io.LimitedReader) {
	switch shape := source.(type) {
	case *os.File:
		return shape, math.MaxInt64, nil
	case *io.LimitedReader:
		if inner, isFile := shape.R.(*os.File); isFile {
			return inner, shape.N, shape
		}
	}
	return nil, 0, nil
}

// sendFile sends up to limit bytes of file through kernel, a sendfileChunk
// at a time.
func (w *Writer) sendFile(kernel io.ReaderFrom, file *os.File, limit int64) (int64, error) {
	var sent int64
	// One reader for the whole stream, refilled for each piece, so the loop
	// allocates nothing.
	piece := &io.LimitedReader{R: file}
	for sent < limit {
		if err := w.roll(); err != nil {
			return sent, w.failed(err)
		}
		asked := min(limit-sent, sendfileChunk)
		piece.N = asked
		count, err := kernel.ReadFrom(piece)
		sent += count
		if err != nil {
			// One call did both the reading and the writing, so the error
			// has to be read to learn whose it is. These are the ways a
			// socket fails; anything else (an I/O error on the disk) is the
			// source's and is left for the caller to treat as a fault.
			if fromTheConnection(err) {
				return sent, w.failed(err)
			}
			return sent, err
		}
		if count < asked {
			// The file ended. Whether that is early is for the caller, who
			// knows what length was promised.
			break
		}
	}
	return sent, nil
}

// fromTheConnection says a sendfile error came from the socket: the deadline
// passed, or the peer closed or reset the connection.
func fromTheConnection(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, net.ErrClosed)
}

// copy moves the stream through a pooled buffer. Here the two ends are
// separate calls, so which one failed is known exactly: a read error is
// returned as it is, and a write error is recorded by Write as the client's.
func (w *Writer) copy(source io.Reader) (int64, error) {
	held := buffers.Get().(*[]byte)
	defer buffers.Put(held)
	buffer := *held

	var sent int64
	for {
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := w.Write(buffer[:read])
			sent += int64(written)
			if writeErr != nil {
				return sent, writeErr
			}
		}
		if readErr == io.EOF {
			return sent, nil
		}
		if readErr != nil {
			return sent, readErr
		}
	}
}
