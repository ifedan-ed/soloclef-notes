// Package humawriter lets a Huma handler answer through an http.ResponseWriter.
//
// This tier's refusals are written by one package, errorenvelope, so that every
// error leaves in the same shape as the Python tier's. That package writes to an
// http.ResponseWriter; a Huma streaming handler is handed a huma.Context. This
// is the adapter between them, and nothing more.
package humawriter

import (
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type writer struct {
	ctx     huma.Context
	headers http.Header
	written bool
}

// New wraps a Huma context. Headers collected before WriteHeader are copied to
// the response at that point, as net/http does.
func New(ctx huma.Context) http.ResponseWriter {
	return &writer{ctx: ctx, headers: http.Header{}}
}

func (w *writer) Header() http.Header {
	return w.headers
}

func (w *writer) WriteHeader(status int) {
	if w.written {
		return
	}
	w.written = true
	for name, values := range w.headers {
		for index, value := range values {
			if index == 0 {
				w.ctx.SetHeader(name, value)
				continue
			}
			w.ctx.AppendHeader(name, value)
		}
	}
	w.ctx.SetStatus(status)
}

func (w *writer) Write(body []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ctx.BodyWriter().Write(body)
}

// Unwrap is the http.ResponseWriter the Huma context writes its body to, so
// that http.ResponseController can reach the connection through this adapter.
//
// A route that streams a file moves the connection's write deadline as the
// bytes go out (internal/rollingdeadline), and it can only do that through a
// chain in which every wrapper offers this method. Without it here the chain
// ended at the first link, and every stream was held to the server's one
// write timeout however well its client was reading.
//
// The router this tier uses (humago) writes straight to net/http's writer,
// so its body writer is one. For an adapter whose body writer is something
// else this returns nil, which ResponseController reports as "not
// supported" -- and a route that needs the deadline then says so and
// refuses, rather than streaming without it.
//
// What is reached this way is for deadlines. Writing or flushing through it
// before WriteHeader would start the response without the headers collected
// here.
func (w *writer) Unwrap() http.ResponseWriter {
	underneath, _ := w.ctx.BodyWriter().(http.ResponseWriter)
	return underneath
}

// ReadFrom copies a whole body from source, and exists so that the copy can
// be handed down: io.Copy asks its destination for this method, and net/http's
// own writer answers it with sendfile when the source is a file. An adapter
// without the method made every file pass through a 32 KB buffer in this
// process, whatever the writers above and below it could do.
//
// The response starts here exactly as it does in Write.
func (w *writer) ReadFrom(source io.Reader) (int64, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	body := w.ctx.BodyWriter()
	if reader, offered := body.(io.ReaderFrom); offered {
		return reader.ReadFrom(source)
	}
	// The body writer cannot take a reader, so the bytes are copied into its
	// Write. It is wrapped to show io.Copy nothing but Write: handing it
	// this writer would bring io.Copy straight back to this method.
	return io.Copy(struct{ io.Writer }{body}, source)
}
