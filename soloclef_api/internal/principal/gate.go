package principal

import (
	"errors"
	"log/slog"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Gate authenticates every request before a route sees it.
//
// There are exactly four outcomes: the caller is known and the route runs; the
// credential was checked and refused, in the refusal's own status and words
// (an agent provider that cannot be read is one of these, a 503); nothing
// identified the caller and it is a 401; or the account could not be read and
// it is a 500.
//
// One of the refusals is the account's only access control: an API key or an
// agent's token whose account has API access switched off is answered 403
// (ErrAPIAccessOff). It is decided here, once, so that no route has to ask.
type Gate struct {
	Resolver Resolver
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (g Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		caller, err := g.Resolver.Resolve(request.Context(), request)
		var refusal Refusal
		switch {
		case err == nil:
			next.ServeHTTP(response, request.WithContext(WithPrincipal(request.Context(), caller)))
		case errors.Is(err, ErrUnauthorized):
			g.Errors.Write(response, http.StatusUnauthorized, "Unauthorized", requestcontext.IDFrom(request))
		case errors.As(err, &refusal):
			g.Errors.Write(response, refusal.Status, refusal.Message, requestcontext.IDFrom(request))
		default:
			// A database that cannot answer "is this account active" is a
			// 500, and the reason stays in the log, not in the answer.
			g.Logger.ErrorContext(request.Context(), "could not resolve the caller",
				slog.String("request_id", requestcontext.IDFrom(request)),
				slog.String("error", err.Error()))
			g.Errors.Write(response, http.StatusInternalServerError,
				errorenvelope.GenericServerMessage, requestcontext.IDFrom(request))
		}
	})
}
