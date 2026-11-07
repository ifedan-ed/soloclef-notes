package requestcontext

import (
	"log/slog"
	"net/http"
	"time"
)

// quietPaths are the probes the Python tier does not log while they succeed.
var quietPaths = map[string]bool{
	"/ready": true, "/health": true, "/healthz": true, "/api/v1/health": true,
}

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
