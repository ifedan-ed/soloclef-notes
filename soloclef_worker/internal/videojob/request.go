package videojob

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The bounds of a request. They are the ones the API tier checks before it
// writes the row (soloclef_assistant/deliverables/video.go), so a
// row that is refused here was written by something other than the chat.
const (
	maxBriefRunes      = 16_000
	maxReferences      = 25
	maxReferenceRunes  = 1_000
	maxSourceRunes     = 180_000
	requestVersion     = 1
	serviceLimitSecond = 600
)

// Request is the job's request column: what to make.
type Request struct {
	// Brief says what the video should be, on one line.
	Brief string
	// SourceReferences name account sources the brief refers to. The chat
	// passes the text itself and leaves this empty.
	SourceReferences []string
	// RootThreadID is the conversation the video was asked for in. It must
	// be the row's own thread.
	RootThreadID int64
	// SourceMaterial is the text the video is written from, with its line
	// breaks, or "".
	SourceMaterial string
	// SourceCondensed says the material was shortened before it was
	// queued, so the author is told.
	SourceCondensed bool
	// MaxSeconds is a cap below the service's own, or 0.
	MaxSeconds int
}

// limitSeconds is how long the video may be: the service's limit, lowered
// by the request's own cap if it has one.
func (r Request) limitSeconds() int {
	if r.MaxSeconds > 0 && r.MaxSeconds < serviceLimitSecond {
		return r.MaxSeconds
	}
	return serviceLimitSecond
}

// readRequest reads and checks the column. Unknown fields are refused, as
// the Python worker's model refused them: a field this worker does not know
// is a writer it was not built for.
func readRequest(raw []byte) (Request, error) {
	var column struct {
		Version            *int     `json:"version"`
		Brief              *string  `json:"brief"`
		SourceReferences   []string `json:"source_references"`
		RevisionArtifactID *int64   `json:"revision_artifact_id"`
		RootThreadID       *int64   `json:"root_thread_id"`
		SourceMaterial     *string  `json:"source_material"`
		SourceCondensed    *bool    `json:"source_condensed"`
		MaxSeconds         *int     `json:"max_seconds"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&column); err != nil {
		return Request{}, fmt.Errorf("the request column is not a video request: %w", err)
	}
	var problems []string
	if column.Version == nil || *column.Version != requestVersion {
		problems = append(problems, "version must be 1")
	}
	var request Request
	if column.Brief != nil {
		request.Brief = strings.Join(strings.Fields(*column.Brief), " ")
	}
	switch runes := utf8.RuneCountInString(request.Brief); {
	case runes == 0:
		problems = append(problems, "the brief is empty")
	case runes > maxBriefRunes:
		problems = append(problems, fmt.Sprintf("the brief is longer than %d characters", maxBriefRunes))
	}
	if len(column.SourceReferences) > maxReferences {
		problems = append(problems, fmt.Sprintf("more than %d source references", maxReferences))
	}
	seen := map[string]bool{}
	for _, reference := range column.SourceReferences {
		normal := strings.Join(strings.Fields(reference), " ")
		switch {
		case normal == "" || strings.ContainsRune(normal, 0):
			problems = append(problems, "a source reference is empty or holds a NUL")
		case utf8.RuneCountInString(normal) > maxReferenceRunes:
			problems = append(problems, fmt.Sprintf("a source reference is longer than %d characters", maxReferenceRunes))
		case seen[normal]:
			problems = append(problems, "a source reference is repeated")
		}
		seen[normal] = true
		request.SourceReferences = append(request.SourceReferences, normal)
	}
	if column.RevisionArtifactID != nil {
		// Nothing queues a revision: the chat makes every video anew. A
		// row that asks for one was not written by it, and is refused by
		// name rather than made as a new video nobody asked for.
		problems = append(problems, "revising an existing video is not something this worker does")
	}
	if column.RootThreadID == nil || *column.RootThreadID < 1 {
		problems = append(problems, "root_thread_id must name the conversation")
	} else {
		request.RootThreadID = *column.RootThreadID
	}
	if column.SourceMaterial != nil {
		request.SourceMaterial = strings.TrimSpace(*column.SourceMaterial)
		switch {
		case request.SourceMaterial == "":
			problems = append(problems, "source_material is present and empty")
		case utf8.RuneCountInString(request.SourceMaterial) > maxSourceRunes:
			problems = append(problems, fmt.Sprintf("source_material is longer than %d characters", maxSourceRunes))
		case strings.ContainsRune(request.SourceMaterial, 0):
			problems = append(problems, "source_material holds a NUL")
		}
	}
	if column.SourceCondensed != nil {
		request.SourceCondensed = *column.SourceCondensed
	}
	if column.MaxSeconds != nil {
		if *column.MaxSeconds < 1 {
			problems = append(problems, "max_seconds must be positive")
		}
		request.MaxSeconds = *column.MaxSeconds
	}
	if len(problems) > 0 {
		return Request{}, errors.New("the video request was refused: " + strings.Join(problems, "; "))
	}
	return request, nil
}
