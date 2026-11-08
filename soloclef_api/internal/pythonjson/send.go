package pythonjson

import (
	"net/http"
	"strconv"
)

// Send writes a rendered body as the Python tier sends it: application/json,
// with a length rather than chunked framing, and nothing else.
func Send(response http.ResponseWriter, status int, body []byte) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	response.WriteHeader(status)
	_, _ = response.Write(body)
}

// SendNoContent writes the 204 a route declared with status_code=204 sends: the
// JSON content type its response class carries, no body, and — deliberately —
// no Content-Length, which is what the Python tier puts on the wire and what
// net/http writes for this status.
func SendNoContent(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusNoContent)
}
