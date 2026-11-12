package studiogenerate

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// SourceDocument is one document a generation reads.
type SourceDocument struct {
	ID    int64
	Title string
	Text  string
}

// The characters of source text each kind of run is given. An artifact run
// reads its material inside a model's context beside its instructions; a
// podcast and a video take the whole text, up to the bound their own
// requests hold (the worker fits it again to the model that runs them).
const (
	artifactMaterialChars   = 60_000
	wholeMaterialChars      = 180_000
	suggestionMaterialChars = 8_000
	maxSourceDocuments      = 500
)

// compileMaterial is every source under its title, each fitted to a fair
// share of the budget. A short source keeps its whole text and hands its
// unused share to the longer ones, so one long document cannot crowd the
// others out, and every source the person ticked is represented.
func compileMaterial(documents []SourceDocument, budget int) string {
	if len(documents) == 0 {
		return ""
	}
	headers := make([]string, len(documents))
	// The blank line between two sources is part of the budget too.
	available := budget - 2*(len(documents)-1)
	for i, document := range documents {
		headers[i] = "## Source: " + oneLine(document.Title) + "\n\n"
		available -= utf8.RuneCountInString(headers[i])
	}
	if available < 0 {
		available = 0
	}
	// Shortest first: what a short source does not use is shared among
	// those still to come.
	order := make([]int, len(documents))
	lengths := make([]int, len(documents))
	for i, document := range documents {
		order[i], lengths[i] = i, utf8.RuneCountInString(document.Text)
	}
	sort.SliceStable(order, func(a, b int) bool { return lengths[order[a]] < lengths[order[b]] })
	shares := make([]int, len(documents))
	for position, index := range order {
		fair := available / (len(order) - position)
		shares[index] = min(lengths[index], fair)
		available -= shares[index]
	}
	parts := make([]string, len(documents))
	for i, document := range documents {
		parts[i] = headers[i] + fit(document.Text, shares[i])
	}
	return strings.Join(parts, "\n\n")
}

// wholeMaterial is every source in full under its title, shortened only
// past the ceiling.
func wholeMaterial(documents []SourceDocument) string {
	parts := make([]string, len(documents))
	for i, document := range documents {
		parts[i] = "## Source: " + oneLine(document.Title) + "\n\n" + document.Text
	}
	return fit(strings.Join(parts, "\n\n"), wholeMaterialChars)
}

// fit is the text when it fits the budget, and otherwise the text shortened
// section by section: each section (a Markdown heading and what follows)
// keeps its heading and the start of its body, in an equal share. A long
// paper cut this way still has its later sections, where a cut at the
// budget would have kept the introduction and lost the results.
func fit(text string, budget int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= budget {
		return text
	}
	if budget <= 0 {
		return ""
	}
	sections := sectionsOf(text)
	share := budget / len(sections)
	var out strings.Builder
	for i, section := range sections {
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(clip(section, share-2))
	}
	// The shares are rounded down and a heading may exceed one, so the
	// whole is held to the budget once more.
	return clip(out.String(), budget)
}

// sectionsOf splits a text at its Markdown headings. A text with none is
// cut into even pieces at paragraph breaks, so that it too keeps something
// from its whole length.
func sectionsOf(text string) []string {
	var sections []string
	var current strings.Builder
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			sections = append(sections, s)
		}
		current.Reset()
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			flush()
		}
		current.WriteString(line)
		current.WriteByte('\n')
	}
	flush()
	if len(sections) > 1 {
		return sections
	}
	const pieces = 8
	paragraphs := strings.Split(text, "\n\n")
	if len(paragraphs) < pieces {
		return []string{text}
	}
	sections = sections[:0]
	for i := 0; i < pieces; i++ {
		from, to := i*len(paragraphs)/pieces, (i+1)*len(paragraphs)/pieces
		sections = append(sections, strings.Join(paragraphs[from:to], "\n\n"))
	}
	return sections
}

// cutMark ends a text that was cut.
const cutMark = " […]"

// clip is the text held to n characters: itself when it fits, otherwise its
// start, ending at a word where it can, with the mark that says it was cut
// counted inside the n.
func clip(text string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	room := n - utf8.RuneCountInString(cutMark)
	if room <= 0 {
		// No room to say so: the bound matters more than the mark.
		return string(runes[:n])
	}
	cut := string(runes[:room])
	if at := strings.LastIndexAny(cut, " \n"); at > len(cut)*3/4 {
		cut = cut[:at]
	}
	return strings.TrimRight(cut, " \n") + cutMark
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
