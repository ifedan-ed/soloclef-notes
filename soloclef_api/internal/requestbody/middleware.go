package requestbody

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

type bufferedKey struct{}

// Middleware reads the body once, hands a request whose body this tier will not
// answer for to elsewhere, and leaves the bytes where the route can read them.
//
// It sits **outside** the authentication, which looks wrong and is the order
// FastAPI decides in. Its request handler calls `await request.json()` in a try
// block that raises RequestValidationError on the spot, and only then solves
// the route's dependencies — so a body that does not decode is a 422 even with
// no credential at all, while a body that decodes and then fails validation is
// a 401 first. Both were asked of the Python tier: `{` with no credential is
// its JSON-decode 422, and `{}` with no credential is its 401.
//
// Nothing here decides anything about a request it can decode: the bytes go in
// the context and the route validates them.
type Middleware struct {
	// Covers is the routes that take a body, so a read is not buffered and a
	// path no route of this tier answers is not touched.
	Covers func(*http.Request) bool
	// Elsewhere answers a body this tier will not answer for — the Python tier,
	// whose own parser places the fault (internal/pythondelegate).
	Elsewhere http.Handler
}

func (m Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Body == nil || !m.Covers(request) {
			next.ServeHTTP(response, request)
			return
		}

		body, err := io.ReadAll(request.Body)
		if err != nil {
			// A body that did not arrive is the Python tier's to answer too:
			// uvicorn reports a disconnect its own way.
			m.Elsewhere.ServeHTTP(response, replay(request, body))
			return
		}
		request.Body.Close()
		request = replay(request, body)

		if _, _, err := Decode(request.Header.Get("Content-Type"), body); err != nil {
			m.Elsewhere.ServeHTTP(response, request)
			return
		}
		next.ServeHTTP(response, request)
	})
}

// replay puts the bytes back where both the route and the Python tier can read
// them: in the context for this tier, and as a fresh Body for a forwarded
// request.
func replay(request *http.Request, body []byte) *http.Request {
	replayed := request.WithContext(context.WithValue(request.Context(), bufferedKey{}, body))
	replayed.Body = io.NopCloser(bytes.NewReader(body))
	replayed.ContentLength = int64(len(body))
	return replayed
}

// Buffered is the body the middleware read, which a route reads instead of the
// stream because the stream has already been consumed.
func Buffered(ctx context.Context) []byte {
	body, buffered := ctx.Value(bufferedKey{}).([]byte)
	if !buffered {
		return nil
	}
	return body
}

// CoveringRoutes matches the method-and-path patterns whose bodies are read
// here, and nothing else.
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
