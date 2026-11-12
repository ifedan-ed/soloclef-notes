package podcasts

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// readTranscript is the transcript a column holds, in the shape the web
// app's `transcript` schema parses: nil when there is none, the rendered
// transcript when there is one, and false for a column that is not one.
//
// The column has two shapes, told apart by JSON type:
//
//   - an object is a Transcript: `{"turns": [{"speaker": 0, "text": "…"}]}`;
//   - a list is a row from before the lifecycle rework: a bare list of turns
//     under their old names, `[{"speaker_id": 0, "dialog": "…"}]`.
//
// A turn may carry keys beside its two. A drafting model decorates turns with
// `emotion` or `speaker_name`, and they are dropped rather than refused.
func readTranscript(column any) (*pythonjson.Object, bool) {
	entries, speakerKey, textKey := []any(nil), "speaker", "text"
	switch held := column.(type) {
	case nil:
		return nil, true
	case []any:
		entries, speakerKey, textKey = held, "speaker_id", "dialog"
	case *pythonjson.Object:
		if !onlyKeys(held, "turns") {
			return nil, false
		}
		turns, isList := held.GetOrNil("turns").([]any)
		if !isList {
			return nil, false
		}
		entries = turns
	default:
		return nil, false
	}
	if len(entries) == 0 {
		// An empty transcript is no transcript: the web app's schema asks
		// for at least one turn.
		if _, legacy := column.([]any); legacy {
			return nil, true
		}
		return nil, false
	}

	turns := make([]any, 0, len(entries))
	for _, entry := range entries {
		written, isObject := entry.(*pythonjson.Object)
		if !isObject {
			return nil, false
		}
		speaker, isNumber := wholeNumber(written.GetOrNil(speakerKey))
		if !isNumber || speaker < 0 {
			return nil, false
		}
		line, isText := written.GetOrNil(textKey).(string)
		if !isText || strings.TrimSpace(line) == "" {
			return nil, false
		}
		turn := pythonjson.NewObject()
		turn.Set("speaker", speaker)
		turn.Set("text", strings.TrimSpace(line))
		turns = append(turns, turn)
	}
	rendered := pythonjson.NewObject()
	rendered.Set("turns", turns)
	return rendered, true
}
