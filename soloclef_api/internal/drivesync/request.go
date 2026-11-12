package drivesync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
)

// Pick is one file chosen in the Google Picker.
type Pick struct {
	ID       string
	Name     string
	MimeType string
}

// request is the import's body, field for field what the web's picker
// sends. It does not say whose documents the picks become: that is the
// caller.
type request struct {
	// Picks are the files, each id once, in the order sent.
	Picks      []Pick
	NotebookID *int64
}

const (
	maxNameRunes = 512
	maxMimeRunes = 255
)

// readRequest reads and checks the body. The string it returns is what is
// wrong with it, for a 422; "" means the request is usable.
//
// Unknown keys are refused: a client that sends one believes it is asking
// for something, and silence would let it go on believing.
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
		Files []struct {
			ID       *string `json:"id"`
			Name     *string `json:"name"`
			MimeType *string `json:"mime_type"`
		} `json:"files"`
		NotebookID *int64 `json:"notebook_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sent); err != nil {
		return request{}, "The request body must be a JSON object with files"
	}
	if decoder.More() {
		return request{}, "The request body must be one JSON object"
	}
	switch {
	case len(sent.Files) == 0:
		return request{}, "Pick at least one file"
	case len(sent.Files) > MaxPickedFiles:
		return request{}, fmt.Sprintf("Pick at most %d files at a time", MaxPickedFiles)
	case sent.NotebookID != nil && *sent.NotebookID < 1:
		return request{}, "notebook_id must be a notebook's number"
	}
	read := request{NotebookID: sent.NotebookID}
	seen := map[string]bool{}
	for _, file := range sent.Files {
		switch {
		// The id goes into a URL's path at Google, so only an id's own
		// characters are let through.
		case file.ID == nil || !drivesyncjob.ValidFileID(*file.ID):
			return request{}, "Every picked file needs the id Google Drive gave it"
		case file.Name == nil || strings.TrimSpace(*file.Name) == "" || utf8.RuneCountInString(*file.Name) > maxNameRunes ||
			strings.ContainsRune(*file.Name, 0):
			return request{}, fmt.Sprintf("Every picked file needs a name of 1 to %d characters", maxNameRunes)
		case file.MimeType != nil && utf8.RuneCountInString(*file.MimeType) > maxMimeRunes:
			return request{}, fmt.Sprintf("A picked file's mime_type is at most %d characters", maxMimeRunes)
		}
		if seen[*file.ID] {
			continue
		}
		seen[*file.ID] = true
		pick := Pick{ID: *file.ID, Name: *file.Name}
		if file.MimeType != nil {
			pick.MimeType = *file.MimeType
		}
		read.Picks = append(read.Picks, pick)
	}
	return read, ""
}
