// Package humawriter lets a Huma handler answer through an http.ResponseWriter.
//
// This tier's refusals are written by one package, errorenvelope, so that every
// error leaves in the same shape as the Python tier's. That package writes to an
// http.ResponseWriter; a Huma streaming handler is handed a huma.Context. This
// is the adapter between them, and nothing more.
package humawriter

import (
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
