// Package errorenvelope writes the error body every Soloclef API error has.
//
// The shape is a contract with the web app and the mobile app, not a detail of
// the Python tier: app.py's _build_error_response wraps every refusal in
// {"error": {...}, "detail": message} and echoes the request id. A route served
// from Go has to refuse requests in the same words.
package errorenvelope

import (
	"net/http"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Writer holds what the envelope needs that is deployment-specific.
type Writer struct {
	// ReportURL is app.exceptions.ISSUES_URL, the contact page of this
	// deployment's own address.
	ReportURL string
	// Now is overridable for tests only.
	Now func() time.Time
}

// Write sends the refusal. The message is the text the Python route would have
// put in HTTPException(detail=...), word for word.
func (w Writer) Write(response http.ResponseWriter, status int, message, requestID string) {
	now := w.Now
	if now == nil {
		now = time.Now
	}

	details := pythonjson.NewObject()
	details.Set("code", CodeForStatus(status))
	details.Set("message", message)
	details.Set("status", status)
	details.Set("request_id", requestID)
	details.Set("timestamp", pythonjson.ISOFormat(now()))
	details.Set("report_url", w.ReportURL)

	body := pythonjson.NewObject()
	body.Set("error", details)
	body.Set("detail", message)

	encoded, err := pythonjson.Marshal(body)
	if err != nil {
		http.Error(response, "internal error", http.StatusInternalServerError)
		return
	}

	response.Header().Set("X-Request-ID", requestID)
	pythonjson.Send(response, status, encoded)
}

// CodeForStatus is app.py's _status_to_code.
func CodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "BAD_REQUEST"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusUnprocessableEntity:
		return "VALIDATION_ERROR"
	case http.StatusTooManyRequests:
		return "RATE_LIMIT_EXCEEDED"
	}
	if status >= 500 {
		return "INTERNAL_ERROR"
	}
	return "CLIENT_ERROR"
}

// GenericServerMessage is app.exceptions.GENERIC_5XX_MESSAGE: a 500 never
// carries the real reason outward.
const GenericServerMessage = "An internal error occurred. Please try again or report this issue if it persists."
