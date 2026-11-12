// Package rendering shapes a tool result into the format the caller asked for.
//
// Tools default to Markdown, which is readable for the model and for the person
// watching, and return raw JSON when a caller wants to post-process the data.
// Large payloads are clipped so one call cannot fill the context window.
package rendering

import (
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

// Format is the value of every tool's response_format parameter.
type Format string

const (
	Markdown Format = "markdown"
	JSON     Format = "json"
)

// FormatParameterDescription is the one description every tool shows for
// response_format, so the published schemas agree with each other.
const FormatParameterDescription = "'markdown' (default, human-readable) or 'json' " +
	"(raw data for post-processing)."

// FormatParameterTitle is the field title the published schema carries.
const FormatParameterTitle = "Response Format"

// DefaultClipChars caps a whole tool result.
const DefaultClipChars = 20_000

// ToJSON pretty-prints a payload the way every tool's JSON form prints it.
func ToJSON(value pythonjson.Value) string {
	return pythonjson.Indented(value, 2)
}

// Clip trims overlong text, leaving a visible marker of how much was dropped.
//
// Counted in characters rather than bytes, because the Python original counts
// Python string length and a byte count would cut a different place.
func Clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	dropped := len(runes) - limit
	return fmt.Sprintf("%s\n\n… [%d more characters truncated]",
		string(runes[:limit]), dropped)
}

// Text is a Markdown document assembled line by line, joined as the Python
// renderers join theirs.
type Text struct{ lines []string }

// Line appends one line.
func (t *Text) Line(format string, arguments ...any) {
	t.lines = append(t.lines, fmt.Sprintf(format, arguments...))
}

// Raw appends one line that is already final, with no formatting applied.
func (t *Text) Raw(line string) { t.lines = append(t.lines, line) }

// Joined is the document.
func (t *Text) Joined() string { return strings.Join(t.lines, "\n") }

// Trimmed is the document with surrounding whitespace removed, which the
// renderers that end a loop with a blank line use.
func (t *Text) Trimmed() string { return strings.TrimSpace(t.Joined()) }

// FormatParameter is the response_format parameter every tool carries, declared
// once so each tool publishes the same name, description and default.
func FormatParameter() toolschema.Parameter {
	return toolschema.Parameter{
		Name:        "response_format",
		Title:       FormatParameterTitle,
		Description: FormatParameterDescription,
		Kind:        toolschema.Text,
		Choices:     []string{string(Markdown), string(JSON)},
		Default:     string(Markdown),
	}
}

// Chosen reads the response_format a call asked for.
func Chosen(arguments toolschema.Arguments) Format {
	return Format(arguments.Text("response_format"))
}
