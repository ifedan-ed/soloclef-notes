package ratelimit

import (
	"log/slog"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Middleware counts each request before the route sees it, as SlowAPIMiddleware
// does: it sits outside the authentication, so an unauthenticated request counts
// and is refused with the same 429 as any other.
type Middleware struct {
	Counter Counter
	Limit   Limit
	// Covers reports whether this request is one the Python tier's limiter
	// would have counted. It exempts what that limiter exempts: a request no
	// route matches is not counted there, because the limiter has no endpoint
	// to count it against.
	Covers func(*http.Request) bool
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (m Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		// Counted by network, not by address, for IPv6 (client_network.go).
		client, path := countedAs(ClientIP(request)), request.URL.Path
		if !m.Covers(request) || client == "" || path == "" {
			next.ServeHTTP(response, request)
			return
		}

		count, err := m.Counter.Hit(request.Context(), Key(m.Limit, client, path), m.Limit.Window, 1)
		if err != nil {
			// Neither counter answered. The Python tier does not swallow that
			// either: the exception leaves the limiter and becomes a 500.
			m.Logger.ErrorContext(request.Context(), "the request could not be counted",
				slog.String("request_id", requestcontext.IDFrom(request)),
				slog.String("error", err.Error()))
			m.Errors.Write(response, http.StatusInternalServerError,
				errorenvelope.GenericServerMessage, requestcontext.IDFrom(request))
			return
		}
		if count > m.Limit.Amount {
			m.Logger.WarnContext(request.Context(), "rate limit exceeded",
				slog.String("client", client), slog.String("path", path))
			response.Header().Set("Retry-After", m.Limit.RetryAfter())
			m.Errors.Write(response, http.StatusTooManyRequests, Message, requestcontext.IDFrom(request))
			return
		}
		next.ServeHTTP(response, request)
	})
}

// CoveringRoutes builds Middleware.Covers from the method-and-path patterns this
// tier serves, so a request the tier does not answer is not counted — which is
// what the Python tier's limiter does with a request none of its routes match.
func CoveringRoutes(patterns ...string) func(*http.Request) bool {
	routes := http.NewServeMux()
	for _, pattern := range patterns {
		routes.Handle(pattern, http.NotFoundHandler())
	}
	return func(request *http.Request) bool {
		_, pattern := routes.Handler(request)
		return pattern != ""
	}
}
