package markdownmemory

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// routesAs serves the three routes over pool behind the middleware cmd/api
// puts in front of them, and returns a call made as a signed-in account: the
// database tests reach the routes as the apps do.
func routesAs(t *testing.T, pool *pgxpool.Pool) func(userID, method, path, body string) (int, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	UserRoutes{Pool: pool, Memory: Store{Pool: pool}, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger}.Register(api)

	var caller string
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: caller, Method: principal.MethodSession})))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes("PUT " + UserWritePath),
	}
	handler := requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	return func(userID, method, path, body string) (int, string) {
		t.Helper()
		caller = userID
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
}
