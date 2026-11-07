package principal

import (
	"errors"
	"log/slog"
	"net/http"

	"git.danvics.com/danvics/surfsense/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/requestcontext"
)

// Gate authenticates every request before a route sees it.
//
// There are exactly three outcomes, and each is one the Python tier also
// produces for the same request: the caller is known and the route runs; the
// credential is one only the Python tier can check, so the request goes there
// unchanged; or nothing identified the caller and it is the same 401.
type Gate struct {
	Resolver Resolver
	Errors   errorenvelope.Writer
	// PythonTier serves the credentials this tier does not verify.
	PythonTier http.Handler
	Logger     *slog.Logger
}

func (g Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		caller, err := g.Resolver.Resolve(request.Context(), request)
		switch {
		case err == nil:
			next.ServeHTTP(response, request.WithContext(WithPrincipal(request.Context(), caller)))
		case errors.Is(err, ErrServedByPythonTier):
			g.PythonTier.ServeHTTP(response, request)
		case errors.Is(err, ErrUnauthorized):
			g.Errors.Write(response, http.StatusUnauthorized, "Unauthorized", requestcontext.IDFrom(request))
		default:
			// A database that cannot answer "is this account active" is a 500
			// here exactly as it is there, and the reason stays in the log.
			g.Logger.ErrorContext(request.Context(), "could not resolve the caller",
				slog.String("request_id", requestcontext.IDFrom(request)),
				slog.String("error", err.Error()))
			g.Errors.Write(response, http.StatusInternalServerError,
				errorenvelope.GenericServerMessage, requestcontext.IDFrom(request))
		}
	})
}
