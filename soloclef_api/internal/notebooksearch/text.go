package notebooksearch

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Query terms, short passages and highlight ranges. Pure functions: the
// clients only draw what comes back, so the web and the phone highlight the
// same way.

// stopwords are words too common to be worth a highlight; the keyword leg
// of the search drops them too.
var stopwords = map[string]bool{}

func init() {
	for _, word := range strings.Fields("a an and are as at be but by for from has have i if in into is it its of on or so than that the their then there these they this to was were what when where which who will with you your") {
		stopwords[word] = true
	}
}

// maxTerms is how many of a query's words are matched and highlighted.
const maxTerms = 8

// snippetChars is the length of a snippet: about three lines of a card.
const snippetChars = 240

// isWord reports whether r is part of a word, as \w is: a letter, a digit
// or an underscore.
func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// queryTerms is the words of query worth matching: lower-cased, each once,
// no stopwords. Underscores and punctuation split words. A single character
// is dropped; it matches too much to mean anything.
func queryTerms(query string) [][]rune {
	var terms [][]rune
	seen := map[string]bool{}
	for _, token := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		runes := []rune(token)
		if len(runes) < 2 || stopwords[token] || seen[token] {
			continue
		}
		seen[token] = true
		if terms = append(terms, runes); len(terms) == maxTerms {
			break
		}
	}
	// Longest first, so "nurses" wins over "nurse" at the same position.
	// Stable, so equal lengths keep the order they were typed in.
	sort.SliceStable(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
	return terms
}

// Each pattern runs over a whole passage, so each must take time in
// proportion to the text. RE2 guarantees that; the classes still stop a
// label at the next "[" and a destination at the next "]" so that a stray
// bracket does not swallow the rest of a line into one "link".
var (
	markdownImage = regexp.MustCompile(`!\[([^\[\]]*)\]\([^)\]]*\)`)
	markdownLink  = regexp.MustCompile(`\[([^\[\]]+)\]\([^)\]]*\)`)
	htmlTag       = regexp.MustCompile(`<[^<>]{1,200}>`)
	markdownMarks = regexp.MustCompile("(?m)(\\*\\*|__|`{1,3}|~~|^#{1,6}\\s+|^>\\s?|\\|)")
)

// plainText is Markdown flattened to one line of readable text: a snippet
// is read, not rendered.
func plainText(markdown string) string {
	text := markdownImage.ReplaceAllString(markdown, "$1")
	text = markdownLink.ReplaceAllString(text, "$1")
	text = htmlTag.ReplaceAllString(text, " ")
	text = markdownMarks.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

// span is a [start, end) range of characters.
type span struct{ start, end int }

// matches is where a term starts a word in text, in order and never
// overlapping. A term must start a word, so "ma" does not light up inside
// "asthma"; it need not end one, so "asth" lights up the start of "asthma"
// while the word is still being typed. terms are lower-case, longest first.
func matches(text []rune, terms [][]rune) []span {
	var found []span
	for i := 0; i < len(text); i++ {
		if i > 0 && isWord(text[i-1]) {
			continue
		}
		for _, term := range terms {
			if hasFoldedPrefix(text[i:], term) {
				found = append(found, span{i, i + len(term)})
				// On to the character after the match; it follows a word
				// character, so the loop passes over the rest of the word.
				i += len(term) - 1
				break
			}
		}
	}
	return found
}

// hasFoldedPrefix reports whether text begins with term, ignoring case.
func hasFoldedPrefix(text, term []rune) bool {
	if len(text) < len(term) {
		return false
	}
	for i, r := range term {
		if unicode.ToLower(text[i]) != r {
			return false
		}
	}
	return true
}

// distinctTerms is how many different terms text holds.
func distinctTerms(text []rune, found []span) int {
	seen := map[string]bool{}
	for _, m := range found {
		seen[strings.ToLower(string(text[m.start:m.end]))] = true
	}
	return len(seen)
}

// densestWindow is where the width-character window holding the most
// distinct terms, then the most matches, starts: at a match, the first such
// window winning. One pass with two ends: found is sorted and never
// overlaps, so the matches inside the window that starts at match i are i
// up to the first one ending past it.
func densestWindow(text []rune, found []span, width int) int {
	words := make([]string, len(found))
	for i, m := range found {
		words[i] = strings.ToLower(string(text[m.start:m.end]))
	}
	counts := map[string]int{}
	best, bestDistinct, bestHits := found[0].start, -1, -1
	end := 0
	for i, m := range found {
		// counts holds the words of found[i:end]; empty when end <= i.
		end = max(end, i)
		for end < len(found) && found[end].end <= m.start+width {
			counts[words[end]]++
			end++
		}
		if distinct, hits := len(counts), end-i; distinct > bestDistinct || distinct == bestDistinct && hits > bestHits {
			best, bestDistinct, bestHits = m.start, distinct, hits
		}
		if end > i {
			if counts[words[i]]--; counts[words[i]] == 0 {
				delete(counts, words[i])
			}
		}
	}
	return best
}

// snapStart moves a cut forward to the start of the next word, if one
// begins within thirty characters, so a snippet does not open mid-word.
func snapStart(text []rune, start int) int {
	if start <= 0 {
		return 0
	}
	for i := start; i < len(text) && i < start+30; i++ {
		if text[i] == ' ' {
			return i + 1
		}
	}
	return start
}

// snapEnd moves a cut back to the end of the last whole word, if one ends
// within thirty characters.
func snapEnd(text []rune, end int) int {
	if end >= len(text) {
		return len(text)
	}
	for i := end - 1; i > 0 && i >= end-30; i-- {
		if text[i] == ' ' {
			return i
		}
	}
	return end
}

// snippet is a short window of plain text around its densest run of
// matches, with "…" where it was cut, and the ranges of the snippet to
// highlight. Without a match (a passage found by meaning) the window is the
// start of the passage.
//
// The ranges count UTF-16 code units, because both clients slice the
// snippet as a JavaScript string and that is what a JavaScript index is.
func snippet(plain string, terms [][]rune) (string, [][2]int) {
	text := []rune(plain)
	if len(text) == 0 {
		return "", [][2]int{}
	}
	start := 0
	if found := matches(text, terms); len(found) > 0 {
		start = max(0, densestWindow(text, found, snippetChars)-snippetChars/4)
	}
	start = snapStart(text, start)
	end := snapEnd(text, min(len(text), start+snippetChars))
	shown := strings.TrimSpace(string(text[start:end]))
	if start > 0 {
		shown = "… " + shown
	}
	if end < len(text) {
		shown += " …"
	}
	runes := []rune(shown)
	// The UTF-16 offset of each character of the snippet, and of its end.
	offsets := make([]int, len(runes)+1)
	for i, r := range runes {
		offsets[i+1] = offsets[i] + utf16.RuneLen(r)
	}
	highlights := [][2]int{}
	for _, m := range matches(runes, terms) {
		highlights = append(highlights, [2]int{offsets[m.start], offsets[m.end]})
	}
	return shown, highlights
}
