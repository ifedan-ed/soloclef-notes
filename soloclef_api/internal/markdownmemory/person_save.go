package markdownmemory

import "strings"

// notesHeading is the section a person's text outside any heading is kept
// under.
const notesHeading = "Notes"

// SaveWrittenByPerson is the save behind the Settings routes (the web page
// and the phone's Memory screen), where a person types the whole document
// themselves. Any text is accepted: the "## heading" rule is the format the
// assistant is asked to write in (Save, which the chat's tool calls), and a
// person was never told about it, so plain text was refused for a reason the
// page could not explain.
//
// The rule, applied to the submitted document (the page sends all of it, so
// the stored copy plays no part):
//
//   - Everything before the first "## " heading is the person's loose text.
//     It goes into the document's "## Notes" section, the first one whose
//     heading is "Notes" in any case, ahead of that section's own lines,
//     because that is where it stood in what they wrote. With no such
//     section, a "## Notes" section is opened at the top, for the same
//     reason. So plain text alone becomes "## Notes" and that text, and a
//     second save of it changes nothing.
//   - Text from the first heading on is kept as written, through the same
//     render as the assistant's writes (a dated bullet in canonical form,
//     trailing spaces and extra blank lines between sections dropped).
//   - The length limit is measured on what will be stored, after the Notes
//     heading is added, because that is what the chat's prompt shows and the
//     prompt shows at most HardLimit characters.
//
// Two things the assistant's save does are deliberately not done here: text
// before the first heading is never dropped, and the NO_UPDATE words are
// ordinary text, since a person who types "No change" means those words.
func SaveWrittenByPerson(requested, stored string) Outcome {
	text := pythonStrip(requested)
	if text == "" {
		// Saving an empty box is a deliberate clear, as the reset route is.
		return Outcome{Write: true}
	}
	rendered := renderDocument(withLooseTextUnderNotes(text))
	if message, refused := tooLong(rendered); refused {
		return Outcome{Refusal: message, Answer: stored}
	}
	return Outcome{Store: rendered, Write: true, Answer: rendered}
}

// withLooseTextUnderNotes parses text with everything before its first
// heading filed under the Notes section, as SaveWrittenByPerson describes.
// The split uses the parser's own line boundaries and heading test, so a
// line counts as a heading here exactly when the parser would open a section
// for it.
func withLooseTextUnderNotes(text string) document {
	lines := pythonSplitLines(text)
	first := len(lines)
	for index, line := range lines {
		if isSectionHeading(line) {
			first = index
			break
		}
	}
	doc := parseDocument(strings.Join(lines[first:], "\n"))
	loose := pythonStrip(strings.Join(lines[:first], "\n"))
	if loose == "" {
		return doc
	}
	// Parsing the loose text under a heading of its own reads its bullets
	// and keeps every other line verbatim, as any section's lines are read.
	notes := parseDocument("## " + notesHeading + "\n" + loose).sections[0]
	for index := range doc.sections {
		if strings.EqualFold(doc.sections[index].heading, notesHeading) {
			doc.sections[index].lines = append(notes.lines, doc.sections[index].lines...)
			return doc
		}
	}
	doc.sections = append([]section{notes}, doc.sections...)
	return doc
}
