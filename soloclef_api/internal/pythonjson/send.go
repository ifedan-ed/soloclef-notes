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
