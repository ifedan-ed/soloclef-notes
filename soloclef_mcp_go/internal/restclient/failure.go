package restclient

import (
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
)

// failureHints turn a status into one actionable sentence for the model. They
// are the Python client's, word for word, because the model reads them.
var failureHints = map[int]string{
	401: "Authentication failed — the Soloclef Notes API key is invalid or expired.",
	402: "The workspace is out of credits for this operation.",
	403: "Access denied — the token lacks permission, or API access is disabled " +
		"for this workspace (enable it in Soloclef Notes workspace settings).",
	404: "The requested resource was not found.",
	429: "Rate limited by the backend — retry after a short pause.",
}

// explainFailure turns an error response into the sentence the Python client
// would have raised.
func explainFailure(status int, body []byte) string {
	detail := extractDetail(body)
	hint, hinted := failureHints[status]
	if detail != "" && hinted {
		return fmt.Sprintf("%s (server said: %s)", hint, detail)
	}
	if detail != "" {
		return fmt.Sprintf("Soloclef Notes returned %d: %s", status, detail)
	}
	if hinted {
		return hint
	}
	return fmt.Sprintf("Soloclef Notes returned HTTP %d.", status)
}

// extractDetail reads the backend's own words out of an error body, following
// the Python client's path: the "detail" member, a "message" inside it when it
// is itself an object, and otherwise the body rendered as Python's str().
//
// A body that is not JSON — an empty one included — takes httpx's own path,
// where response.json() raises and the trimmed text is used instead. An empty
// body therefore yields no detail at all, where decoding it as JSON null would
// have put the word "None" in the message.
func extractDetail(body []byte) string {
	if len(strings.TrimSpace(string(body))) == 0 {
		return ""
	}
	parsed, err := pythonjson.DecodeAny(body)
	if err != nil {
		return strings.TrimSpace(string(body))
	}
	if parsed.Kind != pythonjson.Object {
		return pythonjson.PythonText(parsed)
	}
	detail, present := parsed.Lookup("detail")
	if !present {
		detail = parsed
	}
	if detail.Kind == pythonjson.Object {
		if message, ok := detail.Lookup("message"); ok && pythonjson.PythonText(message) != "" {
			return pythonjson.PythonText(message)
		}
		return pythonjson.PythonText(detail)
	}
	return pythonjson.PythonText(detail)
}
