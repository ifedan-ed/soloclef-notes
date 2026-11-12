package sourceguides

import "strings"

// excerptParts is how many passages a long document is sampled in.
const excerptParts = 6

// excerptGap marks where text was left out, for the model.
const excerptGap = "\n\n[…]\n\n"

// excerpt is text as the model is shown it: all of it when it fits in
// budget characters, otherwise excerptParts passages spaced evenly from the
// first character to the last. A guide says what a document covers, and a
// document cut to its opening pages would be described by its introduction
// alone; sampling the whole length keeps the later sections in the guide.
func excerpt(text string, budget int) string {
	runes := []rune(text)
	if len(runes) <= budget {
		return text
	}
	size := (budget - (excerptParts-1)*len([]rune(excerptGap))) / excerptParts
	parts := make([]string, excerptParts)
	for i := range parts {
		// The first passage starts at the beginning and the last ends at
		// the end; the others are spread evenly between.
		start := (len(runes) - size) * i / (excerptParts - 1)
		parts[i] = strings.TrimSpace(string(runes[start : start+size]))
	}
	return strings.Join(parts, excerptGap)
}
