package gatedanalytics

import (
	"io"
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// mcpClientHeader is the header the MCP server sets, which is how the Python
// tier tells its traffic from a script's.
const mcpClientHeader = "X-SurfSense-Client"

// Sink takes an event away from the request.
type Sink interface {
	Record(Event)
}

// Middleware records every request whose caller is gated, after the response.
//
// It is wrapped *inside* the authentication, as PatApiAnalyticsMiddleware is:
// a request that never resolved a principal is not recorded, a request the
// limiter refused never reaches it, and a request forwarded to the Python tier
// is recorded by that tier instead of twice here.
type Middleware struct {
	Sink Sink
	// Template gives the route template a request matched, so the recorded
	// property stays low-cardinality. It falls back to the request's own path,
	// as the Python middleware does when the route has none.
	Template func(*http.Request) string
}

func (m Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)

		caller, authenticated := principal.FromContext(request.Context())
		if !authenticated || !caller.IsGated() {
			return
		}
		m.Sink.Record(Event{
			Account:    caller.UserID,
			Route:      m.Template(request),
			Method:     request.Method,
			Status:     recorder.status,
			AuthMethod: string(caller.Method),
			Client:     clientLabel(request),
		})
	})
}

func clientLabel(request *http.Request) string {
	if request.Header.Get(mcpClientHeader) == "mcp" {
		return "mcp"
	}
	return "pat_script"
}

// RouteTemplates maps a request to the method-and-path pattern it matched,
// without its method, and to its own path when it matched none.
func RouteTemplates(patterns ...string) func(*http.Request) string {
	routes := http.NewServeMux()
	for _, pattern := range patterns {
		routes.Handle(pattern, http.NotFoundHandler())
	}
	return func(request *http.Request) string {
		_, pattern := routes.Handler(request)
		if pattern == "" {
			return request.URL.Path
		}
		if _, path, found := strings.Cut(pattern, " "); found {
			return path
		}
		return pattern
	}
}

// statusRecorder remembers the status the route answered with, which is one of
// the four properties the event carries.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.written {
		r.status, r.written = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	r.written = true
	return r.ResponseWriter.Write(body)
}

// Unwrap keeps the optional interfaces of the writer underneath reachable.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ReadFrom passes a whole-body copy down to the writer underneath, which for
// a file is how it reaches net/http's sendfile. The recorder's one duty is
// done first, as in Write: once a body has started the status is settled.
// (An event carries the status and no byte count, so there is nothing about
// the body itself to observe on its way past.)
func (r *statusRecorder) ReadFrom(source io.Reader) (int64, error) {
	r.written = true
	if reader, offered := r.ResponseWriter.(io.ReaderFrom); offered {
		return reader.ReadFrom(source)
	}
	// Shown to io.Copy as a plain writer, or it would find this method again.
	return io.Copy(struct{ io.Writer }{r.ResponseWriter}, source)
}
