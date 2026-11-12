package notebooklinks

import (
	"strings"
	"unicode/utf8"
)

// markerWord is what every source chip saved in an answer's text holds:
// "[citation:<where>]", where <where> is a passage's id or a web address
// (internal/chat/cite).
const markerWord = "citation:"

// rewriteMarkers returns text with each source marker replaced by what
// replace answers for the marker's <where>, or removed when it answers
// false. A removed marker takes the spaces and tabs before it along, so
// "see [citation:9]." becomes "see." and not "see .".
//
// A marker is an opening bracket in either style a model writes ("[" or
// "【"), an optional zero-width space, the word, and everything up to the
// closing bracket on the same line. The word in prose, or a marker never
// closed, is text and is kept. Nothing else in the text is touched: this is
// a scan and not a regular expression for the reason the frozen-copy
// builder's is (internal/chat/threads, publicText), which it follows.
func rewriteMarkers(text string, replace func(where string) (string, bool)) string {
	at := strings.Index(text, markerWord)
	if at < 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	rest := text
	for at >= 0 {
		start, opened := markerStart(rest[:at])
		length := -1
		if opened {
			if length = strings.IndexAny(rest[at:], "]】\n"); length >= 0 && rest[at+length] == '\n' {
				length = -1
			}
		}
		if length < 0 {
			b.WriteString(rest[:at+len(markerWord)])
			rest = rest[at+len(markerWord):]
			at = strings.Index(rest, markerWord)
			continue
		}
		where := strings.TrimSuffix(rest[at+len(markerWord):at+length], "​")
		if shown, keep := replace(strings.TrimSpace(where)); keep {
			b.WriteString(rest[:start])
			b.WriteString(shown)
		} else {
			b.WriteString(strings.TrimRight(rest[:start], " \t"))
		}
		// Past the closing bracket, which is one or three bytes long.
		_, size := utf8.DecodeRuneInString(rest[at+length:])
		rest = rest[at+length+size:]
		at = strings.Index(rest, markerWord)
	}
	b.WriteString(rest)
	return b.String()
}

// markerStart reports where a marker ending in the word begins, given the
// text before the word: at an opening bracket, with at most a zero-width
// space between the two.
func markerStart(before string) (int, bool) {
	before = strings.TrimSuffix(before, "​")
	for _, bracket := range []string{"[", "【"} {
		if strings.HasSuffix(before, bracket) {
			return len(before) - len(bracket), true
		}
	}
	return 0, false
}
