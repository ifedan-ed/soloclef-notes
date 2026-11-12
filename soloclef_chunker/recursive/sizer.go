package recursive

// Sizer is the vocabulary a chunk's size is measured in, and the cut the last
// recursion level makes with it.
//
// It is the one seam in this package. Everything else -- which delimiters,
// in which order, with which minimum piece, merged how -- is chonkie's core,
// called directly. What a "size of 512" counts is not: it is whatever
// EMBEDDING_TOKENIZER names on the Python side, so the caller supplies it
// here too rather than this package guessing.
//
// *chonkie.Tokenizer satisfies this as it stands, which is the point: the
// vocabulary that sizes a Go chunk is then the same file the backend image
// bakes, read by the same library.
type Sizer interface {
	// Count is how many units text holds. chonkie calls this
	// `count_tokens` and uses it for every size decision it makes.
	Count(text string) (int, error)

	// SplitOffsets is where to cut text so that each piece holds at most
	// size units, as byte offsets into text itself. It starts at 0 and ends
	// at len(text), so it holds one more entry than there are pieces, and an
	// empty text gives a single entry and so no pieces.
	//
	// Offsets, not pieces, and never a decode: chonkie's own last level
	// encodes the text, slices the ids and decodes each slice back, which
	// for a SentencePiece vocabulary turns every newline into a space and
	// loses a space at some piece boundaries. The Python side overrides that
	// level for the same reason (app/config/verbatim_chunker.py); the two
	// have to cut in the same places.
	SplitOffsets(text string, size int) ([]int, error)
}

// Characters is chonkie's `character` tokenizer: one unit per Unicode code
// point, cutting every size code points.
//
// It is the sizing the Python side uses when EMBEDDING_TOKENIZER is unset,
// and the sizing the chunks stored before 2026-11-09 were cut to. It is a
// legitimate choice a caller makes by name, not a fallback: a Chunker with no
// Sizer refuses to chunk rather than quietly becoming this one.
//
// There is nothing to delegate to the Rust core here. chonkie's character
// tokenizer is Python, not core, and its encode/decode round trip is the
// identity -- which is why the decoding defect that forced the last level to
// be rewritten was invisible until a real vocabulary was named.
type Characters struct{}

// Count is the number of code points in text.
func (Characters) Count(text string) (int, error) {
	count := 0
	for range text {
		count++
	}
	return count, nil
}

// SplitOffsets cuts every size code points. Code points, not bytes: a
// document of em dashes gives pieces of size characters and 3*size bytes.
func (Characters) SplitOffsets(text string, size int) ([]int, error) {
	if size < 1 {
		return nil, errSizeTooSmall(size)
	}
	cuts := []int{0}
	count := 0
	for at := range text {
		if count == size {
			cuts = append(cuts, at)
			count = 0
		}
		count++
	}
	if cuts[len(cuts)-1] != len(text) {
		cuts = append(cuts, len(text))
	}
	return cuts, nil
}
