package markdownmemory

import (
	"strings"
	"unicode"
)

// forbiddenTeamHeadings is validation.py's _FORBIDDEN_TEAM_HEADINGS: the
// headings team memory may not introduce, matched on the normalised form.
var forbiddenTeamHeadings = map[string]bool{
	"preferences":           true,
	"instructions":          true,
	"personal notes":        true,
	"personal instructions": true,
}

// normaliseHeading is document.py:normalize_heading — lowercased, every run of
// non-alphanumeric characters collapsed to one space, and the result trimmed.
// "Personal  Notes!" and "personal notes" are the same heading to the rule.
func normaliseHeading(heading string) string {
	var normalised strings.Builder
	previousWasSpace := true
	for _, character := range strings.ToLower(pythonStrip(heading)) {
		if pythonIsAlphanumeric(character) {
			normalised.WriteRune(character)
			previousWasSpace = false
			continue
		}
		if !previousWasSpace {
			normalised.WriteRune(' ')
			previousWasSpace = true
		}
	}
	return strings.TrimFunc(normalised.String(), unicode.IsSpace)
}

// explicitHeadings is document.py:extract_headings — the normalised headings of
// the sections that carry one, which is what the scope rule compares.
func explicitHeadings(content string) map[string]bool {
	headings := map[string]bool{}
	for _, part := range parseDocument(content).sections {
		if part.explicitHeading {
			headings[normaliseHeading(part.heading)] = true
		}
	}
	return headings
}
