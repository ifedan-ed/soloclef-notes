package nextcloudimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// request is the body both the preview and the import take, field for
// field what the web's picker sends. It does not say whose documents the
// picks become: that is the caller.
type request struct {
	Picks []Pick
	// Parser is the submitted name, "" when the field was left out or
	// null: the deployment's named default then applies
	// (parserchoice.Choose).
	Parser     string
	Vision     bool
	NotebookID *int64
}

// maxPathLength is the longest picked path accepted, in characters.
const maxPathLength = 4096

// readRequest reads and checks the body. The string it returns is what is
// wrong with it, for a 422; "" means the request is usable.
//
// Unknown keys are refused: a client that sends one believes it is asking
// for something, and silence would let it go on believing. A key naming an
// owner is unknown like any other.
func readRequest(body io.Reader) (request, string) {
	// One byte past the limit, to tell a body of exactly the limit from a
	// longer one without reading whatever was sent.
	raw, err := io.ReadAll(io.LimitReader(body, bodyLimit+1))
	if err != nil {
		return request{}, "The request body could not be read"
	}
	if len(raw) > bodyLimit {
		return request{}, "The request body is too large"
	}
	var sent struct {
		Items []struct {
			Path *string `json:"path"`
			Kind *string `json:"kind"`
		} `json:"items"`
		Parser     *string `json:"parser"`
		Vision     *bool   `json:"use_vision_llm"`
		NotebookID *int64  `json:"notebook_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sent); err != nil {
		return request{}, "The request body must be a JSON object with items"
	}
	if decoder.More() {
		return request{}, "The request body must be one JSON object"
	}

	var read request
	switch {
	case len(sent.Items) == 0:
		return request{}, "Pick at least one file or folder"
	case len(sent.Items) > maxPickedItems:
		return request{}, fmt.Sprintf("Pick at most %d items at a time", maxPickedItems)
	case sent.NotebookID != nil && *sent.NotebookID < 1:
		return request{}, "notebook_id must be a notebook's number"
	}
	read.NotebookID = sent.NotebookID
	for _, item := range sent.Items {
		switch {
		case item.Path == nil || *item.Path == "" || utf8.RuneCountInString(*item.Path) > maxPathLength:
			return request{}, fmt.Sprintf("Every picked item needs a path of 1 to %d characters", maxPathLength)
		case item.Kind == nil || (*item.Kind != "file" && *item.Kind != "folder"):
			return request{}, `Every picked item's kind must be "file" or "folder"`
		}
		read.Picks = append(read.Picks, Pick{Path: *item.Path, Folder: *item.Kind == "folder"})
	}
	if sent.Parser != nil {
		read.Parser = *sent.Parser
	}
	read.Vision = sent.Vision != nil && *sent.Vision
	return read, ""
}
