package recursive

import "strings"

// The shortest piece a delimiter cut may leave, chonkie's
// `min_characters_per_chunk` at its default. Its name says characters, but
// chonkie-core measures it against a byte slice, so a run of em dashes cuts
// every eight characters rather than every twenty-four. Counting it in
// characters here would move a boundary in every document that is not ASCII.
const minPieceBytes = 24

// splitAtDelimiters cuts text after each delimiter, as chonkie-core's
// split_offsets and split_pattern_offsets do with include_delim="prev": the
// delimiter stays at the end of the piece it closed.
//
// A cut is only taken where the piece it would close and the piece that would
// follow it both reach minPieceBytes, so the short pieces around a one-word
// line are absorbed into their neighbours instead of being emitted alone.
func splitAtDelimiters(text string, delimiters []string) []string {
	return absorbShortPieces(cutAfterEachDelimiter(text, delimiters))
}

func cutAfterEachDelimiter(text string, delimiters []string) []string {
	var pieces []string
	start := 0
	for i := 0; i < len(text); {
		width := delimiterAt(text[i:], delimiters)
		if width == 0 {
			i++
			continue
		}
		i += width
		pieces = append(pieces, text[start:i])
		start = i
	}
	if start < len(text) {
		pieces = append(pieces, text[start:])
	}
	return pieces
}

// delimiterAt reports the width of the first delimiter that matches here,
// trying them in the order the level lists them.
func delimiterAt(text string, delimiters []string) int {
	for _, delimiter := range delimiters {
		if strings.HasPrefix(text, delimiter) {
			return len(delimiter)
		}
	}
	return 0
}

func absorbShortPieces(pieces []string) []string {
	var out []string
	var current strings.Builder
	for i, piece := range pieces {
		current.WriteString(piece)
		if i+1 >= len(pieces) {
			continue
		}
		if current.Len() >= minPieceBytes && len(pieces[i+1]) >= minPieceBytes {
			out = append(out, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}
