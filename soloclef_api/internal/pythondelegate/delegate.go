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
		proxy.ServeHTTP(response, request)
	}), nil
}
