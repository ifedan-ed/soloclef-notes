package artifacts

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// decodeColumn is a json or jsonb column's value: nil for a SQL NULL and for
// the JSON null alike. A column that will not decode is read as nil too --
// Postgres only stores valid JSON, so this is a column from somewhere this
// code does not know, and one such row must not cost a caller the rest.
func decodeColumn(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	value, err := pythonjson.Decode(raw)
	if err != nil {
		return nil
	}
	return value
}

// objectOf is a column that is meant to hold an object, as one: the object
// when it is one, and an empty object for anything else.
//
// Both columns these routes read (`documents.status`, `artifacts.metadata`)
// are free-form jsonb written by several tasks. A row holding a list or a
// string where an object belongs used to raise, and in the list that was a
// 500 for the whole list. It is now a row with no state and no metadata.
func objectOf(value any) *pythonjson.Object {
	if object, isObject := value.(*pythonjson.Object); isObject {
		return object
	}
	return pythonjson.NewObject()
}

// present says a slide names audio under this key: the key holds text that
// is not empty. A number, a truth or an object is not a storage key or a
// path.
func present(value any) bool {
	text, isText := value.(string)
	return isText && strings.TrimSpace(text) != ""
}

// integer is a JSON integer, as its digits. A float, text and a truth are
// not ids or slide numbers.
func integer(value any) (pythonjson.RawInt, bool) {
	number, isInteger := value.(pythonjson.RawInt)
	return number, isInteger
}
