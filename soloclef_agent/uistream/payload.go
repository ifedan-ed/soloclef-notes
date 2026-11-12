package uistream

import (
	"bytes"
	"encoding/json"
	"errors"
)

// objectBody returns what is between the braces of a JSON object, compacted,
// so that a field can be added after it. A tool's question must be an
// object, because the client reads fields from it; anything else is a tool
// bug, reported as the stream's error instead of sent as a frame no card can
// read.
func objectBody(raw []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil || compact.Len() < 2 || compact.Bytes()[0] != '{' {
		return nil, errors.New("uistream: a tool's question must be a JSON object")
	}
	out := compact.Bytes()
	return out[1 : len(out)-1], nil
}
