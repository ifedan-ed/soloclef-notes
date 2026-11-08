package recursive

// The hierarchy chonkie's RecursiveRules defaults to, in its order: a text too
// long for one chunk is cut at paragraph breaks, then at sentence ends, then
// at the punctuation that marks a pause, then between words, and finally
// mid-word at a fixed number of characters.
//
// Order matters twice over. A later level is only reached by a piece that is
// still too long, and within a level the delimiters are tried in this order at
// each position, so a position where two of them match is cut by the one
// listed first.
var defaultLevels = []level{
	{delimiters: []string{"\n\n", "\r\n", "\n", "\r"}},
	{delimiters: []string{". ", "! ", "? "}},
	{delimiters: []string{
		"{", "}", `"`, "[", "]", "<", ">", "(", ")",
		":", ";", ",", "—", "|", "~", "-", "...", "`", "'",
	}},
	// chonkie spells this level `whitespace=True`, which reaches
	// chonkie-core's byte splitter with a single space as the delimiter set.
	{delimiters: []string{" "}},
	// The last level has no delimiter: it cuts every chunkSize characters.
	{},
}

// level is one step of the hierarchy: the delimiters it cuts at, or none.
type level struct {
	delimiters []string
}

func (l level) cutsAtDelimiters() bool { return len(l.delimiters) > 0 }
