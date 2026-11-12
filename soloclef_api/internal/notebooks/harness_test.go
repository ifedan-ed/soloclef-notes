package notebooks

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

const testOrigin = "https://notes.example"

// bodyReader is the API's body reader over this package's bodies.
func bodyReader() requestbody.Middleware {
	return requestbody.Middleware{Covers: requestbody.CoveringRoutes(WithBodies()...)}
}

// caller is who a test request is from; the gate that resolves a credential
// to one is internal/principal's and is not under test here.
type caller struct {
	userID string
	method principal.Method
}

// served is the routes as cmd/api mounts them: request id, CORS, the body
// reader, the search's wrapper, then the caller and the routes.
func served(database DBTX, who *caller, now func() time.Time) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	Routes{
		Database: database,
		Errors:   errorenvelope.Writer{ReportURL: "https://notes.example/contact"},
		Logger:   logger,
		Now:      now,
	}.Register(humago.New(mux, huma.DefaultConfig("test", "0.0.1")))

	var answer http.Handler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := principal.WithPrincipal(request.Context(), principal.Principal{UserID: who.userID, Method: who.method})
		mux.ServeHTTP(response, request.WithContext(ctx))
	})
	answer = bodyReader().Wrap(answer)
	return requestcontext.Middleware(logger, corsmirror.NewPolicy([]string{testOrigin}).Middleware(answer))
}
