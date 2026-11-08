// Package pythondelegate hands a request to the Python API tier unchanged.
//
// It exists so that moving a route here is never all-or-nothing: a request this
// tier cannot answer itself — today, one bearing a credential only the Python
// tier can verify — is served by the Python tier, with its response passed back
// untouched. The caller cannot tell which tier answered, which is the same
// property the Go worker tier relies on (custom/docs/go-worker.md).
package pythondelegate

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
)

// hopHeader marks a request this tier has already forwarded. A deployment that
// points this tier's upstream at itself would otherwise forward forever; with
// the marker the second hop is refused instead.
const hopHeader = "X-Soloclef-Api-Delegated"

// ErrLoop is that refusal.
var ErrLoop = errors.New("the Python upstream points back at this tier")

// New returns a handler that forwards to the upstream's base URL.
func New(upstream string, logger *slog.Logger, onError func(http.ResponseWriter, *http.Request, error)) (http.Handler, error) {
	base, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("python upstream: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("python upstream %q needs a scheme and a host", upstream)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(base)
			// The Python tier reads the caller's address from the proxy's
			// headers (app.rate_limiter.get_real_client_ip), so they are
			// forwarded rather than replaced.
			request.Out.Host = request.In.Host
			request.Out.Header.Set(hopHeader, "1")
		},
		ErrorHandler: func(response http.ResponseWriter, request *http.Request, err error) {
			logger.Error("could not reach the Python tier",
				slog.String("path", request.URL.Path), slog.String("error", err.Error()))
			// This tier is answering after all, so its own CORS headers apply.
			corsmirror.Allow(request.Context())
			onError(response, request, err)
		},
	}

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get(hopHeader) != "" {
			logger.Error("refusing to forward a request this tier already forwarded",
				slog.String("path", request.URL.Path))
			onError(response, request, ErrLoop)
			return
		}
		// A forwarded response's CORS headers are the **upstream's**, because
		// the upstream is the tier that answered. Suppressing here keeps
		// internal/corsmirror from adding this tier's when the response
		// starts, so the upstream's are the only ones; the ErrorHandler above
		// allows them again, because on an unreachable upstream it is this
		// tier that answers after all.
		//
		// Two things made this necessary rather than cosmetic. Until
		// 2026-11-08 internal/corsmirror wrote its headers *before* the
		// handler ran, and net/http adds rather than replaces, so a forwarded
		// cross-origin response carried each of them **twice** — and a browser
		// rejects a response with two Access-Control-Allow-Origin values
		// outright, which made the whole delegation path unusable from the web
		// app from phase 2 onward. And keeping this tier's instead of the
		// upstream's would still have been wrong: Starlette answers a 500 its
		// route did not catch from *outside* its CORS middleware and so sends
		// no CORS headers at all, and a body only the Python tier's parser can
		// place is exactly such a 500.
		corsmirror.Suppress(request.Context())
		proxy.ServeHTTP(response, request)
	}), nil
}
