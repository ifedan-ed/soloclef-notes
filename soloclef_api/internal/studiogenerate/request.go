package studiogenerate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// The bounds of the fields every dialog has.
const (
	maxSourceIDs     = 500
	maxFocusChars    = 2000
	maxTitleChars    = 200
	maxLanguageChars = 40
	maxRequestID     = 64
	maxStylePrompt   = 300
	maxTemplateKey   = 64
	maxTemplateChars = 4000
)

var requestIDShape = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// choice is one preset a kind's dialog offers: the values it may take, the
// first being what an absent field means.
type choice struct {
	field  string
	values []string
}

// The kinds and the presets each dialog can send. The values are presets,
// not free numbers, so the dialog and the task written from it agree on a
// small vocabulary; this table is the whole of it.
var kindChoices = map[string][]choice{
	"audio":      {{"format", []string{"deep_dive", "brief", "critique", "debate"}}, {"length", []string{"default", "short", "long"}}},
	"video":      {{"format", []string{"explainer", "short"}}, {"visual_style", []string{"auto", "custom", "classic", "whiteboard", "playful", "dark", "minimal"}}},
	"quiz":       {{"count", []string{"standard", "fewer", "more"}}, {"difficulty", []string{"medium", "easy", "hard"}}},
	"flashcards": {{"count", []string{"standard", "fewer", "more"}}, {"difficulty", []string{"medium", "easy", "hard"}}},
	"mindmap":    {{"detail", []string{"standard", "overview", "detailed"}}},
	"slides":     {{"format", []string{"detailed", "presenter"}}, {"length", []string{"default", "short", "long"}}},
	"data_table": {},
	// The styles and the orientations are the specialist's own lists, so
	// the dialog can never send one it would refuse.
	"infographic": {{"style", deliverables.InfographicStyles()}, {"orientation", deliverables.InfographicOrientations()}, {"detail", []string{"standard", "concise", "detailed"}}},
	"report":      {{"format", []string{"document", "interactive"}}, {"file_type", []string{"pdf", "docx"}}},
}

// freeText is the kind's own free-text fields beside the shared ones, and
// the most characters each may hold.
var freeText = map[string]map[string]int{
	"video":  {"style_prompt": maxStylePrompt},
	"report": {"template_key": maxTemplateKey, "template_prompt": maxTemplateChars},
}

// Request is a dialog's request, checked.
type Request struct {
	Kind string
	// SourceIDs are notebook source rows; nil means the ticked ones.
	SourceIDs []int64
	Focus     string
	Title     string
	Language  string
	// RequestID is the client's own id for the click, or "".
	RequestID string
	// Options is the kind's presets, every one present, and its free text
	// where given.
	Options map[string]string
}

// Stored is the request as the output row keeps it and the panel reads it
// back: everything but the sources, the title and the click's id.
func (r Request) Stored() ([]byte, error) {
	stored := map[string]any{"kind": r.Kind, "focus": r.Focus}
	if r.Language != "" {
		stored["language"] = r.Language
	}
	for field, value := range r.Options {
		stored[field] = value
	}
	return json.Marshal(stored)
}

// parseRequest reads a generate body. Every problem is reported at once, in
// the envelope's own failure shape, and a field the kind does not have is
// a problem: a dialog that sends one believes something this route does not
// do.
func parseRequest(raw []byte) (Request, []errorenvelope.Failure) {
	var failures []errorenvelope.Failure
	fail := func(field, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"body", field}, Message: message})
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return Request{}, []errorenvelope.Failure{{Location: []string{"body"}, Message: "The body must be a JSON object"}}
	}
	text := func(field string, most int) string {
		value, present := fields[field]
		delete(fields, field)
		if !present || string(value) == "null" {
			return ""
		}
		var s string
		switch {
		case json.Unmarshal(value, &s) != nil:
			fail(field, "Input should be a valid string")
		case utf8.RuneCountInString(s) > most:
			fail(field, fmt.Sprintf("String should have at most %d characters", most))
		case strings.ContainsRune(s, 0):
			fail(field, "String must not contain a NUL character")
		}
		return s
	}

	request := Request{Kind: text("kind", 32), Options: map[string]string{}}
	choices, known := kindChoices[request.Kind]
	if !known {
		kinds := make([]string, 0, len(kindChoices))
		for kind := range kindChoices {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		return Request{}, []errorenvelope.Failure{{Location: []string{"body", "kind"}, Message: "Input should be one of: " + strings.Join(kinds, ", ")}}
	}
	request.Focus = strings.TrimSpace(text("focus", maxFocusChars))
	request.Title = strings.TrimSpace(text("title", maxTitleChars))
	request.Language = strings.TrimSpace(text("language", maxLanguageChars))
	request.RequestID = text("request_id", maxRequestID)
	if request.RequestID != "" && !requestIDShape.MatchString(request.RequestID) {
		fail("request_id", "String should contain only letters, digits, '_' and '-'")
	}
	if value, present := fields["source_ids"]; present && string(value) != "null" {
		// Present and empty is "no sources", which is refused later as
		// such; absent is "the ticked ones".
		request.SourceIDs = []int64{}
		switch {
		case json.Unmarshal(value, &request.SourceIDs) != nil:
			fail("source_ids", "Input should be a list of integers")
		case len(request.SourceIDs) > maxSourceIDs:
			fail("source_ids", fmt.Sprintf("List should have at most %d items", maxSourceIDs))
		}
	}
	delete(fields, "source_ids")

	for _, c := range choices {
		value := text(c.field, 64)
		if value == "" {
			value = c.values[0]
		}
		if !contains(c.values, value) {
			fail(c.field, "Input should be one of: "+strings.Join(c.values, ", "))
		}
		request.Options[c.field] = value
	}
	for field, most := range freeText[request.Kind] {
		if value := strings.TrimSpace(text(field, most)); value != "" {
			request.Options[field] = value
		}
	}
	for field := range fields {
		fail(field, "Extra inputs are not permitted")
	}

	if request.Kind == "video" {
		// The description belongs to the custom style and to no other.
		described := strings.Join(strings.Fields(request.Options["style_prompt"]), " ")
		delete(request.Options, "style_prompt")
		switch {
		case request.Options["visual_style"] != "custom":
		case described == "":
			fail("style_prompt", "A custom visual style needs a description")
		default:
			request.Options["style_prompt"] = described
		}
	}
	if request.Kind == "report" && request.Options["format"] != "document" {
		// The file type only means something for a document report.
		delete(request.Options, "file_type")
	}
	// Reported in a stable order: a map was ranged over on the way here.
	sort.SliceStable(failures, func(i, j int) bool {
		return failures[i].Location[len(failures[i].Location)-1] < failures[j].Location[len(failures[j].Location)-1]
	})
	if len(failures) > 0 {
		return Request{}, failures
	}
	return request, nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
