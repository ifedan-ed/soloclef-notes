package documentdeletion

import (
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// NotFound is the 404's wording, which names the id the caller asked for.
func NotFound(documentID int64) string {
	return "Document with id " + strconv.FormatInt(documentID, 10) + " not found"
}

// Message is the route's whole response body.
func Message() *pythonjson.Object {
	body := pythonjson.NewObject()
	body.Set("message", Deleted)
	return body
}
