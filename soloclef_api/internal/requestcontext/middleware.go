package requestcontext

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// quietPaths are the probes the Python tier does not log while they succeed.
var quietPaths = map[string]bool{
	"/ready": true, "/health": true, "/healthz": true, "/api/v1/health": true,
}

// clientClosedRequest is the status logged for a request whose client left
// before anything was answered.
const clientClosedRequest = 499

// statusRecorder remembers the status so the access line can report it. The
// Python middleware assumes 500 until the response starts, for the same reason.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

// Unwrap keeps the writer underneath reachable to http.ResponseController.
// This recorder is the outermost wrapper of every response, so without it
// nothing behind it could move the connection's write deadline, and a file
// stream was cut at the server's write timeout however well its client was
// reading (internal/rollingdeadline).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ReadFrom passes a whole-body copy down to the writer underneath, which for
// a file is how it reaches net/http's sendfile. What this recorder is for is
// kept: a body that starts without a status is a 200, as in Write.
func (r *statusRecorder) ReadFrom(source io.Reader) (int64, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if reader, offered := r.ResponseWriter.(io.ReaderFrom); offered {
		return reader.ReadFrom(source)
	}
	// Shown to io.Copy as a plain writer, or it would find this method again.
	return io.Copy(struct{ io.Writer }{r.ResponseWriter}, source)
}

// Middleware accepts or mints the request id, echoes it on the response, and
// writes one access line per request.
func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := Accept(request.Header.Get(Header))
		response.Header().Set(Header, requestID)

		recorder := &statusRecorder{ResponseWriter: response, status: 0}
		started := time.Now()
		next.ServeHTTP(recorder, request.WithContext(WithID(request.Context(), requestID)))

		status := recorder.status
		if status == 0 {
			status = http.StatusInternalServerError
			// Nothing was written because there was nobody to write to: the
			// client closed the connection first, which cancels the request's
			// context, and a route that notices says nothing (a video player
			// does this on every seek). Calling that a 500 would put a
			// warning in the log for each one. 499 is the number nginx gave
			// "client closed request", and is what an operator reading
			// access lines already takes it to mean. It is only ever logged;
			// no client is sent it.
			if errors.Is(request.Context().Err(), context.Canceled) {
				status = clientClosedRequest
			}
		}
		if quietPaths[request.URL.Path] && status < 400 {
			return
		}
		level := slog.LevelInfo
		if status >= 500 || status == http.StatusTooManyRequests {
			level = slog.LevelWarn
		}
		logger.LogAttrs(request.Context(), level, "request",
			slog.String("request_id", requestID),
			slog.String("method", request.Method),
			slog.String("path", request.URL.Path),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
		)
	})
}
