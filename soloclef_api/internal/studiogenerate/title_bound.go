package studiogenerate

import (
	"strings"
	"unicode"
)

// This file holds a title to the bound every Studio title is held to
// (maxTitleChars) by cutting it, never by refusing it.
//
// A title is cut and not refused because the clients build it themselves
// as "<kind label>: <notebook title>", and a notebook's title may be up to
// 200 characters: the default title of a notebook titled past about 185
// characters is longer than the bound, and refusing it refused every
// generate in that notebook with a 422. The installed phone builds send that
// title and cannot be changed, so the server takes it and cuts it.

// cutTitle is title on one line and at most maxTitleChars characters.
//
// It counts and cuts runes, so a multi-byte character is never split. A cut
// falls on a whole-word boundary: at the last white space within the bound,
// when the cut would otherwise end inside a word. A script written without
// spaces (Chinese, Japanese) has no word boundary to find, and neither does
// one word longer than half the bound; then the title is cut at the bound
// itself, because backing off further would leave a title that says almost
// nothing.
func cutTitle(title string) string {
	title = oneLine(title)
	runes := []rune(title)
	if len(runes) <= maxTitleChars {
		return title
	}
	kept := runes[:maxTitleChars]
	if !unicode.IsSpace(runes[maxTitleChars]) {
		// The cut is inside a word: back off to the space before it.
		if space := lastSpace(kept); space >= maxTitleChars/2 {
			kept = kept[:space]
		}
	}
	return strings.TrimSpace(string(kept))
}

// lastSpace is the index of the last white space in runes, or -1.
func lastSpace(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return i
		}
	}
	return -1
}
