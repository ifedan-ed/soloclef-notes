package requestbody

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

type bufferedKey struct{}

// Middleware reads the body once, refuses one that is not JSON at all, and
// leaves the bytes where the route can read them.
//
// It sits **outside** the authentication, which looks wrong and is the order
// FastAPI decides in, kept so that clients see the answers they always saw.
// FastAPI's request handler calls `await request.json()` in a try block that
// raises RequestValidationError on the spot, and only then solves the route's
// dependencies — so a body that does not decode is a 422 even with no
// credential at all, while a body that decodes and then fails validation is a
// 401 first: `{` with no credential is the JSON-decode 422, and `{}` with no
// credential is the 401.
//
// Nothing here decides anything about a request it can decode: the bytes go in
// the context and the route validates them.
type Middleware struct {
	// Covers is the routes that take a body, so a read is not buffered and a
	// path no route answers is not touched.
	Covers func(*http.Request) bool
	// Errors writes the refusals: the 413 for a body past MaxBufferedBytes,
	// the 422 for one that is not JSON and the 400 for one that is not text.
	// Its zero value is a usable writer; a deployment passes its own for the
	// report link.
	Errors errorenvelope.Writer
}

func (m Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Body == nil || !m.Covers(request) {
			next.ServeHTTP(response, request)
			return
		}

		// Bounded: this runs before the credential is checked (size_limit.go).
		body, err := readBounded(response, request)
		if errors.Is(err, errTooLarge) {
			request.Body.Close()
			refuseTooLarge(m.Errors, response, request)
			return
		}
		request.Body.Close()
		if err != nil {
			// The body did not arrive whole: the client went away, or sent a
			// chunked body that broke off. A 400, because what was sent cannot
			// be read, and FastAPI's words for a body it could not read. Most
			// often nobody is left to receive it, which costs nothing.
			m.Errors.Write(response, http.StatusBadRequest, UnparseableBody, requestcontext.IDFrom(request))
			return
		}
		request = replay(request, body)

		if _, _, err := Decode(request.Header.Get("Content-Type"), body); err != nil {
			refuseUndecodable(m.Errors, response, request, err)
			return
		}
		next.ServeHTTP(response, request)
	})
}

// refuseUndecodable answers a body Decode could not read as JSON: a 400 for
// bytes that are not UTF-8, which are not text at all (FastAPI answered the
// same, from its catch-all around the parse), and the 422 validation envelope
// at the body for anything else, which is what a client already handles for a
// bad field.
func refuseUndecodable(writer errorenvelope.Writer, response http.ResponseWriter, request *http.Request, err error) {
	requestID := requestcontext.IDFrom(request)
	if errors.Is(err, ErrNotUTF8) {
		writer.Write(response, http.StatusBadRequest, UnparseableBody, requestID)
		return
	}
	writer.WriteValidation(response, requestID, []errorenvelope.Failure{{
		Location: []string{BodyRoot},
		Message:  JSONDecodeError,
	}})
}

// replay puts the bytes back where the route can read them: in the context,
// and as a fresh Body for a handler that reads the stream.
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
