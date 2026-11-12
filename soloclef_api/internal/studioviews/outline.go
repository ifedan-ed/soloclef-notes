package studioviews

import (
	"regexp"
	"strings"
)

// A Markdown report split into the sections its table of contents lists.
//
// The web viewer splits the same Markdown by the same rule
// (soloclef_web/features/notebooks/lib/report-outline.ts), so a suggestion
// stored against section n sits under the section the reader sees as n.
// The rule: a leading `#` heading is the title; the contents list is the
// shallowest heading level that occurs at least twice after it (or the
// shallowest present, when none repeats); a heading inside fenced code is
// not a heading. Change one side and the cards move to the wrong section,
// which is why outline_test.go holds the cases the web's own test holds.

var (
	headingLine = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	fenceLine   = regexp.MustCompile("^[ \t]{0,3}(`{3,}|~{3,})")
	// "1. ", "2) ", "1.2 ", "IV. ": not a bare number ("2024 in review").
	numbering = regexp.MustCompile(`^(?:\d+(?:\.\d+)*[.)]|\d+(?:\.\d+)+|[IVXLC]+\.)\s+`)
	emphasis  = regexp.MustCompile("[*_`]+")
)

// section is one entry of the contents list.
type section struct {
	// index counts from 0, as the web numbers them.
	index   int
	heading string
	// text is everything from the heading line to the next listed heading.
	text string
}

// body is the section without its heading line.
func (s section) body() string {
	_, rest, found := strings.Cut(s.text, "\n")
	if !found {
		return ""
	}
	return strings.TrimSpace(rest)
}

// outline is a report as its reading view lays it out.
type outline struct {
	// title is "" when the report does not open with a `#` heading.
	title    string
	sections []section
}

// headingLabel is a heading as the contents list shows it: no emphasis
// marks, no numbering.
func headingLabel(raw string) string {
	text := strings.TrimSpace(emphasis.ReplaceAllString(raw, ""))
	if stripped := strings.TrimSpace(numbering.ReplaceAllString(text, "")); stripped != "" {
		return stripped
	}
	return text
}

type heading struct {
	line, level int
	text        string
}

// headingsOf is every heading outside fenced code.
func headingsOf(lines []string) []heading {
	var found []heading
	fence := ""
	for number, line := range lines {
		if marker := fenceLine.FindStringSubmatch(line); marker != nil {
			token := marker[1]
			switch {
			case fence == "":
				fence = token
			case token[0] == fence[0] && len(token) >= len(fence):
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if match := headingLine.FindStringSubmatch(line); match != nil {
			found = append(found, heading{number, len(match[1]), strings.TrimSpace(match[2])})
		}
	}
	return found
}

// splitReport is the report's outline.
func splitReport(markdown string) outline {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	headings := headingsOf(lines)
	var out outline
	if len(headings) > 0 && headings[0].level == 1 &&
		strings.TrimSpace(strings.Join(lines[:headings[0].line], "\n")) == "" {
		out.title = headingLabel(headings[0].text)
		headings = headings[1:]
	}
	if len(headings) == 0 {
		return out
	}
	counts := map[int]int{}
	for _, h := range headings {
		counts[h.level]++
	}
	// The shallowest level that repeats, else the shallowest there is.
	listed, repeats := 7, false
	for level, count := range counts {
		switch {
		case count >= 2 && (!repeats || level < listed):
			listed, repeats = level, true
		case !repeats && level < listed:
			listed = level
		}
	}
	var starts []heading
	for _, h := range headings {
		if h.level == listed {
			starts = append(starts, h)
		}
	}
	for i, h := range starts {
		// Up to the next listed heading, so nothing between two is dropped.
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1].line
		}
		out.sections = append(out.sections, section{
			index: i, heading: headingLabel(h.text),
			text: strings.TrimSpace(strings.Join(lines[h.line:end], "\n")),
		})
	}
	return out
}
