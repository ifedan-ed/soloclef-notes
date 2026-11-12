// Package uistream writes a run's events as the server-sent-event stream the
// Soloclef web and phone apps read.
//
// The frames are those of the Vercel AI SDK's UI message stream, but the
// contract kept here is narrower and stricter than that protocol: it is what
// the apps' own readers accept (soloclef_web/lib/chat/streaming-state.ts and
// its phone twin), frame for frame and in the order they depend on. The
// library's loop knows none of this. If the client protocol changes, this
// package changes and nothing else does.
//
// The ordering rules, each of which has a test:
//
//  1. The stream opens with "start" then "start-step", and closes with
//     "finish-step", "finish", "data: [DONE]". The chat layer's own frames
//     (turn info, message ids, status) go between, through Data.
//  2. A reasoning block is closed before text starts, text before reasoning
//     starts, and both before a tool call starts.
//  3. One tool call uses one toolCallId on every frame about it.
//  4. What a nested agent's model says is never sent. Only the top-level
//     run's text is the answer; a nested agent shows as tool cards.
//  5. A waiting call is one "data-interrupt-request" frame, in the order
//     the calls were made, carrying the id its decision must be sent back
//     with.
//  6. "error" ends the stream.
//  7. Silence is broken by a comment line; see KeepAlive.
//
// Rule 1 has a half this package cannot keep by itself: the chat layer's
// opening frames must be written before the run is started, and its closing
// status frame before End. The Writer is what the chat layer calls in that
// order; the end-to-end test shows the order.
package uistream

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// Config is what a Writer needs. Every field but Metadata must be set.
type Config struct {
	// Out receives the stream. Flush is called after every frame so that a
	// frame reaches the reader when it is written, not when a buffer fills;
	// for an http.ResponseWriter it is http.NewResponseController(w).Flush.
	Out   io.Writer
	Flush func() error

	// Now stamps the start and end of reasoning blocks.
	Now func() time.Time

	// NewID names a text or reasoning block; it is given "text" or
	// "reasoning". RandomID is the implementation for production.
	NewID func(kind string) string

	// Card decides what a finished tool call shows. Whatever it returns is
	// sent to the browser, saved in the message, and may be shared, so it is
	// an allowlist by construction: a tool's output goes nowhere unless a
	// Card puts it there. LengthOnly is the card that lets nothing through.
	Card func(result event.Event) any

	// Approval describes a call that is waiting for approval, for the card
	// that asks the person.
	Approval func(waiting event.Event) ApprovalCard

	// Metadata, when set, is asked for a value to attach to each tool frame
	// as "metadata". Optional; a nil result attaches nothing.
	Metadata func(tool event.Event) any
}

// ApprovalCard is the part of an approval request only the chat layer knows.
type ApprovalCard struct {
	// InterruptType picks the card the client mounts, e.g. "permission_ask".
	InterruptType string

	// Context is extra data for that card. Optional.
	Context any
}

// LengthOnly is the card for a tool with none of its own: a status and the
// size of the result, and nothing of its content.
func LengthOnly(result event.Event) any {
	return lengthOnly{Status: "completed", ResultLength: len([]rune(result.Text))}
}

type lengthOnly struct {
	Status       string `json:"status"`
	ResultLength int    `json:"result_length"`
}

// RandomID returns kind, an underscore, and 32 random hex digits: the form
// the clients have always been sent.
func RandomID(kind string) string {
	var b [16]byte
	// crypto/rand.Read does not fail on any platform Go supports; it aborts
	// the program rather than return short.
	_, _ = rand.Read(b[:])
	return kind + "_" + hex.EncodeToString(b[:])
}

// Writer turns events into frames. Its methods may be called from any
// goroutine; frames are written whole and one at a time.
type Writer struct {
	cfg Config

	mu  sync.Mutex
	buf []byte
	err error

	// textID and reasoningID name the block that is open, or are empty.
	textID      string
	reasoningID string

	// closed is set by End and Fail. Nothing is written after either: the
	// reader has been told the stream is over, and a late frame (an event
	// from a run still unwinding, a keep-alive that lost a race) would be
	// bytes after the end marker.
	closed bool

	// lastWrite is when the last frame went out, for KeepAlive.
	lastWrite time.Time
}

// New checks cfg and returns a writer.
func New(cfg Config) (*Writer, error) {
	switch {
	case cfg.Out == nil:
		return nil, errors.New("uistream: Out is not set")
	case cfg.Flush == nil:
		return nil, errors.New("uistream: Flush is not set")
	case cfg.Now == nil:
		return nil, errors.New("uistream: Now is not set")
	case cfg.NewID == nil:
		return nil, errors.New("uistream: NewID is not set; uistream.RandomID is the usual choice")
	case cfg.Card == nil:
		return nil, errors.New("uistream: Card is not set; uistream.LengthOnly shows nothing of a tool's output")
	case cfg.Approval == nil:
		return nil, errors.New("uistream: Approval is not set")
	}
	return &Writer{cfg: cfg, buf: make([]byte, 0, 512), lastWrite: cfg.Now()}, nil
}

// Err returns the first error from writing or encoding, if any. After an
// error nothing more is written: a reader that has gone away has gone away.
// Whether the run should go on without it is the caller's decision, made by
// cancelling the run's context or not.
func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Begin opens the stream for the assistant message with the given id.
func (w *Writer) Begin(messageID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frame(func(b []byte) []byte {
		return appendString(append(b, `{"type": "start", "messageId": `...), messageID)
	})
	w.literal(`{"type": "start-step"}`)
}

// Data writes one of the chat layer's own frames, {"type": "data-<name>",
// "data": payload}.
func (w *Writer) Data(name string, payload any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frameErr(func(b []byte) ([]byte, error) {
		b = appendString(append(b, `{"type": `...), "data-"+name)
		return appendValue(append(b, `, "data": `...), payload)
	})
}

// End closes the stream of a turn that finished or paused. Any block still
// open is closed first.
func (w *Writer) End() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeBlocks()
	w.literal(`{"type": "finish-step"}`)
	w.literal(`{"type": "finish"}`)
	w.done()
}

// Fail closes the stream of a turn that failed. message is shown to the
// person and must be written for them; errorCode is for the client to branch
// on and may be empty.
func (w *Writer) Fail(message, errorCode string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeBlocks()
	w.frame(func(b []byte) []byte {
		b = appendString(append(b, `{"type": "error", "message": `...), message)
		if errorCode != "" {
			b = appendString(append(b, `, "errorCode": `...), errorCode)
		}
		return b
	})
	w.done()
}

// frame writes one "data:" line whose JSON is built by build, which appends
// to the buffer it is given and ends without the closing brace.
func (w *Writer) frame(build func([]byte) []byte) {
	w.frameErr(func(b []byte) ([]byte, error) { return build(b), nil })
}

func (w *Writer) frameErr(build func([]byte) ([]byte, error)) {
	if w.err != nil || w.closed {
		return
	}
	b, err := build(append(w.buf[:0], "data: "...))
	if err != nil {
		w.err = err
		return
	}
	w.buf = append(b, '}', '\n', '\n')
	w.flush()
}

// literal writes a frame whose JSON is fixed.
func (w *Writer) literal(json string) {
	if w.err != nil || w.closed {
		return
	}
	w.buf = append(append(append(w.buf[:0], "data: "...), json...), '\n', '\n')
	w.flush()
}

func (w *Writer) done() {
	if w.err != nil || w.closed {
		return
	}
	w.buf = append(w.buf[:0], "data: [DONE]\n\n"...)
	w.flush()
	w.closed = true
}

func (w *Writer) flush() {
	if _, err := w.cfg.Out.Write(w.buf); err != nil {
		w.err = err
		return
	}
	if err := w.cfg.Flush(); err != nil {
		w.err = err
		return
	}
	w.lastWrite = w.cfg.Now()
}
