package markdownmemory

import "strings"

// defaultLegacySection is document.py's DEFAULT_LEGACY_SECTION: the heading a
// document made only of bullets is given, so `- 2026-01-01: x` alone comes back
// as `## Memory` with that bullet under it.
const defaultLegacySection = "Memory"

// line is one rendered line of a section: a dated bullet, or text kept
// verbatim.
type line struct {
	bullet   bullet
	isBullet bool
	raw      string
}

// section is a "## " heading and the lines under it. explicitHeading is false
// for the section the parser invents for a headingless document, and only an
// explicit one counts as the heading a saved document must have.
type section struct {
	heading         string
	lines           []line
	explicitHeading bool
}

// document is app/services/memory/document.py's MemoryDocument.
type document struct {
	sections []section
}

// isSectionHeading is document.py:is_section_heading.
func isSectionHeading(text string) bool {
	return strings.HasPrefix(text, "## ") && pythonStrip(text[3:]) != ""
}

// headingText is document.py:heading_text.
func headingText(text string) string {
	return pythonStrip(text[3:])
}

// parseDocument is document.py:parse_memory_document.
//
// Two details decide the bytes and neither is obvious: the content is stripped
// and split with Python's own rules (python_text.go), so U+2028 and the C0
// separators are line boundaries; and a line before the first heading is
// *dropped* unless it parses as a bullet, in which case the parser opens an
// implicit "Memory" section. That is why `short prose` with no heading is
// re-rendered as the empty document — a data loss this tier reproduces rather
// than corrects, because the route it replaces does it.
func parseDocument(content string) document {
	if content == "" {
		return document{}
	}

	var sections []section
	var currentHeading string
	haveHeading := false
	currentExplicit := true
	var currentLines []line

	flush := func() {
		if !haveHeading {
			return
		}
		sections = append(sections, section{
			heading:         currentHeading,
			lines:           currentLines,
			explicitHeading: currentExplicit,
		})
		currentHeading = ""
		haveHeading = false
		currentExplicit = true
		currentLines = nil
	}

	for _, rawLine := range pythonSplitLines(pythonStrip(content)) {
		text := pythonRightStrip(rawLine)
		if isSectionHeading(text) {
			flush()
			currentHeading = headingText(text)
			haveHeading = true
			currentExplicit = true
			currentLines = nil
			continue
		}

		parsed, isBullet := parseBullet(text)
		if !haveHeading {
			if !isBullet {
				continue
			}
			currentHeading = defaultLegacySection
			haveHeading = true
			currentExplicit = false
			currentLines = []line{{bullet: parsed, isBullet: true}}
			continue
		}
		if isBullet {
			currentLines = append(currentLines, line{bullet: parsed, isBullet: true})
			continue
		}
		currentLines = append(currentLines, line{raw: text})
	}
	flush()
	return document{sections: sections}
}

// renderDocument is document.py:render_memory_document: each section as its
// heading and its lines, a bullet written canonically whatever form it arrived
// in, sections joined by a blank line, and every join stripped.
func renderDocument(doc document) string {
	rendered := make([]string, 0, len(doc.sections))
	for _, part := range doc.sections {
		lines := []string{"## " + part.heading}
		for _, item := range part.lines {
			if item.isBullet {
				lines = append(lines, "- "+renderDate(item.bullet.day)+": "+item.bullet.text)
				continue
			}
			lines = append(lines, item.raw)
		}
		rendered = append(rendered, pythonStrip(strings.Join(lines, "\n")))
	}
	kept := make([]string, 0, len(rendered))
	for _, part := range rendered {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return pythonStrip(strings.Join(kept, "\n\n"))
}

// hasExplicitHeading is MemoryDocument.has_explicit_heading.
func hasExplicitHeading(content string) bool {
	for _, part := range parseDocument(content).sections {
		if part.explicitHeading {
			return true
		}
	}
	return false
}
