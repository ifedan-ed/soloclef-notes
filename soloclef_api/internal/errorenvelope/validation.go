package errorenvelope

import (
	"net/http"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Failure is one bad parameter, in the shape app.py's _validation_error_handler
// puts in error.fields: the location path pydantic reports and its message.
type Failure struct {
	Location []string
	Message  string
}

// WriteValidation sends the 422 a FastAPI route sends for a parameter it cannot
// parse: one message joining every failure, and the failures themselves under
// error.fields so a client can show them per field.
func (w Writer) WriteValidation(response http.ResponseWriter, requestID string, failures []Failure) {
	now := w.Now
	if now == nil {
		now = time.Now
	}

	summaries := make([]string, 0, len(failures))
	fields := make([]any, 0, len(failures))
	for _, failure := range failures {
		summaries = append(summaries, summarise(failure))

		location := make([]any, 0, len(failure.Location))
		for _, part := range failure.Location {
			location = append(location, part)
		}
		field := pythonjson.NewObject()
		field.Set("loc", location)
		field.Set("msg", failure.Message)
		fields = append(fields, field)
	}
	message := "Validation failed: " + strings.Join(summaries, "; ")

	details := pythonjson.NewObject()
	details.Set("code", "VALIDATION_ERROR")
	details.Set("message", message)
	details.Set("status", http.StatusUnprocessableEntity)
	details.Set("request_id", requestID)
	details.Set("timestamp", pythonjson.ISOFormat(now()))
	details.Set("report_url", w.ReportURL)
	details.Set("fields", fields)

	body := pythonjson.NewObject()
	body.Set("error", details)
	body.Set("detail", message)

	encoded, err := pythonjson.Marshal(body)
	if err != nil {
		http.Error(response, "internal error", http.StatusInternalServerError)
		return
	}
	response.Header().Set("X-Request-ID", requestID)
	pythonjson.Send(response, http.StatusUnprocessableEntity, encoded)
}

// summarise is _validation_error_handler's own _segment: the location path
// joined with arrows and the message after it, with the "body" request root
// dropped so a field reads "name: Field required" rather than
// "body -> name: ...", and a failure of the model itself reads as a plain
// sentence with no location at all. The root stays in error.fields, which is
// what a client attaches the message to.
func summarise(failure Failure) string {
	path := failure.Location
	if len(path) > 0 && path[0] == "body" {
		path = path[1:]
	}
	if len(path) == 0 {
		return failure.Message
	}
	return strings.Join(path, " -> ") + ": " + failure.Message
}

// IntegerExpected is the message pydantic gives for a parameter that is not a
// whole number, which is the refusal every path and query parameter handed over
// so far shares.
const IntegerExpected = "Input should be a valid integer, unable to parse string as an integer"
