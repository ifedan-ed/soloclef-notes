package recursive

import (
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/chonkie"
)

// MinCharactersPerChunk is chonkie's `min_characters_per_chunk` at its
// default, which is what this app leaves it at.
//
// A delimiter cut is only taken where the piece it closes and the piece that
// follows it both reach this, so the short pieces around a one-word line are
// absorbed into their neighbours instead of being emitted alone.
//
// Its name says characters and chonkie's core measures it in BYTES. A run of
// em dashes therefore cuts every eight characters, not every twenty-four.
// That number is not passed through a character count anywhere here: it goes
// to the core exactly as chonkie passes it, and the core does what it does.
const MinCharactersPerChunk = 24

// The hierarchy chonkie's RecursiveRules defaults to, in its order: a text too
// long for one chunk is cut at paragraph breaks, then at sentence ends, then
// at the punctuation that marks a pause, then between words, and finally
// mid-word at a fixed number of tokens.
//
// Order matters twice over. A later level is only reached by a piece that is
// still too long, and within a level the core matches the delimiters
// leftmost-first, so a position where two of them match is cut by the one
// listed first.
//
// Read out of the running backend rather than copied from documentation:
//
//	python -c "from chonkie.types import RecursiveRules
//	for lv in RecursiveRules().levels: print(lv.delimiters, lv.whitespace, lv.include_delim)"
var defaultLevels = []level{
	{delimiters: []string{"\n\n", "\r\n", "\n", "\r"}, includeDelim: chonkie.IncludePrev},
	{delimiters: []string{". ", "! ", "? "}, includeDelim: chonkie.IncludePrev},
	{delimiters: []string{
		"{", "}", `"`, "[", "]", "<", ">", "(", ")",
		":", ";", ",", "—", "|", "~", "-", "...", "`", "'",
	}, includeDelim: chonkie.IncludePrev},
	// chonkie spells this level `whitespace=True`, which reaches the core's
	// single-byte splitter with one space as the whole delimiter set.
	{whitespace: true, includeDelim: chonkie.IncludePrev},
	// The last level has no delimiter and no whitespace flag: it cuts every
	// chunkSize tokens. See Chunker.cutAtTokens.
	{includeDelim: chonkie.IncludePrev},
}

// level is one step of the hierarchy, as chonkie's RecursiveLevel spells it.
type level struct {
	delimiters   []string
	whitespace   bool
	includeDelim chonkie.IncludeDelim
}

// splitSpans cuts text the way chonkie's `_split_text` cuts it at this level,
// as byte spans into text.
//
// Only a level that cuts at text answers here. The token level is the
// caller's business, because it is the one level that needs a vocabulary.
func (l level) splitSpans(text string) ([]chonkie.Span, error) {
	switch {
	case len(l.delimiters) > 0 && l.hasMultiByteDelimiter():
		return chonkie.SplitAtPatterns(text, l.delimiters, l.includeDelim, MinCharactersPerChunk)
	case len(l.delimiters) > 0:
		// chonkie joins the delimiters and encodes the result, handing the
		// core a byte set rather than a list of strings.
		return chonkie.SplitAtDelimiters(text, strings.Join(l.delimiters, ""), l.includeDelim, MinCharactersPerChunk)
	case l.whitespace:
		return chonkie.SplitAtDelimiters(text, " ", l.includeDelim, MinCharactersPerChunk)
	default:
		return nil, errNotATextLevel
	}
}

// hasMultiByteDelimiter decides which of the core's two splitters chonkie
// sends this level to, and it is a trap worth spelling out.
//
// chonkie's test is `any(len(d) > 1 for d in delimiters)` on Python strings,
// so it counts CHARACTERS. An em dash is one character and three bytes, so a
// level holding em dashes and nothing longer would go to the single-byte
// splitter -- where the dash is torn into three unrelated delimiter bytes,
// each of which cuts inside a character. The punctuation level above survives
// that only because "..." sits in the same list and is three characters long,
// which flips the whole level to the pattern splitter.
//
// So this counts runes, exactly as chonkie does, rather than bytes. Counting
// bytes here would look more careful and would send a level to a different
// splitter than Python sends it to, which is a different set of boundaries.
func (l level) hasMultiByteDelimiter() bool {
	for _, delimiter := range l.delimiters {
		if utf8.RuneCountInString(delimiter) > 1 {
			return true
		}
	}
	return false
}

// cutsAtText reports whether this level cuts at text rather than at tokens.
// chonkie branches on exactly this in `_recursive_chunk`: the token level's
// pieces are left as they are instead of being merged back up, because they
// are already cut to the ceiling.
func (l level) cutsAtText() bool {
	return len(l.delimiters) > 0 || l.whitespace
}
