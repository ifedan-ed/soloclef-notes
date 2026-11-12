package corsmirror

import (
	"context"
	"io"
	"net/http"
)

// Starlette has **two** error handlers and only one of them is inside the CORS
// middleware, so not every response of the Python tier carries the CORS
// headers even when the request carried an Origin.
//
//	ExceptionMiddleware   answers an HTTPException a route raised.
//	                      It sits INSIDE CORSMiddleware, so its refusal —
//	                      a 401, a 403, a 404, a 422, and a 500 a route
//	                      caught and re-raised itself — gets the headers.
//	ServerErrorMiddleware answers everything a route did NOT catch, and is
//	                      the OUTERMOST middleware of all, so its 500 goes
//	                      out with no CORS headers at all. A failure raised
//	                      while the response is being serialised is one of
//	                      these, because it happens after the route returned.
//
// The difference is per route and was measured rather than reasoned about:
//
//	GET /api/v1/logs/2200000000              -> 500, CORS headers present
//	                                            (read_log catches its own)
//	DELETE /api/v1/pats/2200000000           -> 500, none
//
// Suppress is how a route says "this is the second kind". It cannot be done by
// deleting the headers afterwards, because a Huma handler writes through a
// huma.Context rather than the http.ResponseWriter the middleware holds — so
// Middleware defers setting them until the response starts and reads this flag
// then.

type suppressedKey struct{}

// WithSuppression gives the request a flag a handler can set. Middleware adds
// it, so a handler can always call Suppress.
func WithSuppression(ctx context.Context) context.Context {
	suppressed := false
	return context.WithValue(ctx, suppressedKey{}, &suppressed)
}

// Suppress says this response must carry no CORS headers, because the Python
// tier would have written it from outside its CORS middleware.
func Suppress(ctx context.Context) {
	if flag, ok := ctx.Value(suppressedKey{}).(*bool); ok {
		*flag = true
	}
}

// Allow undoes a Suppress, for the one caller that has to: the delegate
// suppresses before forwarding so the upstream's headers are the only ones, and
// allows again if the upstream turns out to be unreachable and this tier writes
// the refusal itself after all.
func Allow(ctx context.Context) {
	if flag, ok := ctx.Value(suppressedKey{}).(*bool); ok {
		*flag = false
	}
}

func suppressed(ctx context.Context) bool {
	flag, ok := ctx.Value(suppressedKey{}).(*bool)
	return ok && *flag
}

// deferredHeaders writes the CORS headers when the response starts, which is
// the only moment at which the flag above is settled.
type deferredHeaders struct {
	http.ResponseWriter
	add     func(http.Header)
	started bool
}

func (d *deferredHeaders) WriteHeader(status int) {
	if !d.started {
		d.started = true
		d.add(d.Header())
	}
	d.ResponseWriter.WriteHeader(status)
}

func (d *deferredHeaders) Write(body []byte) (int, error) {
	if !d.started {
		d.WriteHeader(http.StatusOK)
	}
	return d.ResponseWriter.Write(body)
}

// Unwrap is how http.ResponseController reaches the real writer, which is what
// a route that ever needs to flush or to set a deadline should use. This
// wrapper deliberately does not implement http.Flusher itself: nothing here
// flushes as it goes (the routes that stream a file send one body of a known
// length), and claiming the interface would make httputil.ReverseProxy flush
// a response it has no reason to.
func (d *deferredHeaders) Unwrap() http.ResponseWriter {
	return d.ResponseWriter
}

// ReadFrom passes a whole-body copy down to the writer underneath, which for
// a file is how it reaches net/http's sendfile. The headers this wrapper
// exists to add are added first, exactly as in Write: a body that arrives
// this way starts the response too.
//
// It is not the Flusher that Unwrap's comment declines. Nothing consults
// ReadFrom unless a handler copies a reader into the response, and the copy
// it then does is the one the handler asked for.
func (d *deferredHeaders) ReadFrom(source io.Reader) (int64, error) {
	if !d.started {
		d.WriteHeader(http.StatusOK)
	}
	if reader, offered := d.ResponseWriter.(io.ReaderFrom); offered {
		return reader.ReadFrom(source)
	}
	// Shown to io.Copy as a plain writer, or it would find this method again.
	return io.Copy(struct{ io.Writer }{d.ResponseWriter}, source)
}
