package markdownmemory

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// The three validations save_memory runs before it writes, in its order. Each
// returns the Python route's message word for word; the route turns it into a
// 400.

// strippedPreamble is validation.py:strip_preamble_to_first_heading, which
// drops whatever a model wrote before the document's first heading.
//
// Note which split it uses: splitlines here but no strip beforehand, and the
// join is stripped. A document with no heading at all is returned stripped.
func strippedPreamble(content string) string {
	lines := pythonSplitLines(content)
	for index, text := range lines {
		if strings.HasPrefix(text, "## ") && pythonStrip(text[3:]) != "" {
			return pythonStrip(strings.Join(lines[index:], "\n"))
		}
	}
	return pythonStrip(content)
}

// tooLong is validation.py:validate_memory_size. The numbers are grouped with
// commas because Python's format spec is "{:,}", and the length is counted in
// characters because Python's len() of a str counts code points.
func tooLong(content string) (string, bool) {
	length := utf8.RuneCountInString(content)
	if length <= HardLimit {
		return "", false
	}
	return "Memory exceeds " + grouped(HardLimit) + " character limit (" +
		grouped(length) + " chars). Consolidate by merging related items, " +
		"removing outdated entries, and shortening descriptions.", true
}

// headingless is validation.py:validate_heading_sanity: content the document
// cannot hold is refused whatever its length, because the document stores the
// render of what it parsed and content it parses nothing out of renders to the
// empty string. An empty submission is still a deliberate clear, and legacy
// bullets still parse.
func headingless(content string) (string, bool) {
	stripped := pythonStrip(content)
	if stripped == "" {
		return "", false
	}
	if hasExplicitHeading(stripped) {
		return "", false
	}
	if len(parseDocument(stripped).sections) > 0 {
		return "", false
	}
	return "Memory must be markdown with at least one ## heading.", true
}

// grouped is Python's "{:,}" for a non-negative count.
func grouped(count int) string {
	digits := strconv.Itoa(count)
	var text strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			text.WriteByte(',')
		}
		text.WriteRune(digit)
	}
	return text.String()
}
