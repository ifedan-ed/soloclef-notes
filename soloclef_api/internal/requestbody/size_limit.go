package requestbody

import (
	"errors"
	"io"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// MaxBufferedBytes is the largest body Middleware will hold in memory.
//
// Middleware reads a body whole and runs before the credential is looked
// at (it must, to answer in FastAPI's order), so without a bound any
// stranger could make this process hold as many bytes as they can send in
// the server's read timeout, once per connection. It is a fact of the
// routes rather than a deployment setting: the largest body any covered
// route takes is a note's Markdown (internal/notes.MaxTextBytes, 2 MiB),
// which JSON's escaping of newlines and quotes can at most about double.
// Eight MiB is well past that and still a small cost per request.
const MaxBufferedBytes = 8 << 20

// tooLargeMessage is the sentence a refused body is answered with.
const tooLargeMessage = "The request body is too large."

// errTooLarge marks a body that passed MaxBufferedBytes.
var errTooLarge = errors.New("requestbody: the body is larger than MaxBufferedBytes")

// readBounded reads the whole body or reports errTooLarge.
//
// A body whose Content-Length already says it is too large is refused on
// its word, with not one byte read. Otherwise http.MaxBytesReader stops at
// one byte past the bound, so a chunked body costs at most the bound; it
// also tells the server to close the connection after the answer rather
// than drain the rest of what the client keeps sending.
func readBounded(response http.ResponseWriter, request *http.Request) ([]byte, error) {
	if request.ContentLength > MaxBufferedBytes {
		return nil, errTooLarge
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, MaxBufferedBytes))
	var overflow *http.MaxBytesError
	if errors.As(err, &overflow) {
		return nil, errTooLarge
	}
	return body, err
}

// refuseTooLarge answers 413 in the error envelope.
func refuseTooLarge(writer errorenvelope.Writer, response http.ResponseWriter, request *http.Request) {
	writer.Write(response, http.StatusRequestEntityTooLarge, tooLargeMessage, requestcontext.IDFrom(request))
}
