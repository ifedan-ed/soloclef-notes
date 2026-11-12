package threads

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// encodeParts is public parts as the JSON a link stores them as.
func encodeParts(parts []any) (string, error) {
	raw, err := pythonjson.Marshal(parts)
	return string(raw), err
}
