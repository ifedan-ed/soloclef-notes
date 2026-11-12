// Package recursive reproduces chonkie's RecursiveChunker, as this app
// configures it, exactly: the same cuts in the same places, byte for byte.
//
// The boundaries it decides are already in the database against the chunks
// and vectors they produced, so this is not an implementation of recursive
// chunking -- it is a reproduction of one, and a cut that lands one character
// from chonkie's is a corpus that no longer matches its index.
//
// How it reproduces them
// ----------------------
// By calling chonkie's own code. Both of the core's splitters, and the merge
// back up to the ceiling, are the `chunk` crate pinned to the version the
// installed chonkie-core wraps, reached through ../chonkie. They used to be
// written out again in Go here, which was correct as far as it was tested and
// was a standing invitation to drift: a reimplementation agrees until the day
// upstream changes a tie-break nobody thought to test.
//
// What is still Go, and why:
//
//   - The level hierarchy (levels.go). It is configuration, not algorithm:
//     chonkie builds it in Python and hands the core one level at a time.
//   - Which of the core's two splitters a level goes to. That decision is
//     chonkie's Python, not the core's, and it has a trap in it worth
//     reproducing deliberately -- see level.hasMultiByteDelimiter.
//   - The recursion itself: which level comes next, and when a piece is too
//     long to leave alone. Also chonkie's Python (`_recursive_chunk`).
//   - Characters, chonkie's `character` tokenizer, which is Python too.
//
// What it is NOT is a chunker that decides its own unit. See Sizer.
package recursive

import (
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/chonkie"
)

// DefaultChunkSize is the ceiling the app runs with: EMBEDDING_CHUNK_SIZE,
// left at chonkie's own default of 512 (the Python app's
// app/config/chunk_sizing.py).
//
// It is a number of whatever unit the Sizer counts, and that is the whole of
// what it says. 512 with a Characters sizer is 512 code points, which is what
// the chunks stored before 2026-11-09 were cut to; 512 with the baked
// BAAI/bge-m3 vocabulary is 512 of that model's tokens, roughly five times as
// much text per chunk, which is what the Python chunker writes today. Pick
// the Sizer that matches the corpus being compared against -- the number
// alone does not say which.
const DefaultChunkSize = 512

// Chunker cuts text into pieces of at most Size units, where the unit is
// whatever Sizer counts.
//
// Both fields are required. A zero Chunker refuses to chunk rather than
// choosing a size or a vocabulary on the caller's behalf: the vocabulary
// decides every boundary, so guessing one would quietly produce a corpus that
// does not match its index.
type Chunker struct {
	Size  int
	Sizer Sizer
}

// errNotATextLevel is the token level reaching code that only handles levels
// which cut at text. It is a programming mistake, not an input one.
var errNotATextLevel = errors.New("recursive: this level cuts at tokens, not at text")

func errSizeTooSmall(size int) error {
	return fmt.Errorf("recursive: chunk size must be at least 1, got %d", size)
}

// Chunks returns the chunk texts in document order.
//
// Their concatenation is the input: no level drops or rewrites a character,
// including the last one, which cuts the text at token boundaries rather than
// rebuilding it from token ids.
func (c Chunker) Chunks(text string) ([]string, error) {
	if c.Size < 1 {
		return nil, errSizeTooSmall(c.Size)
	}
	if c.Sizer == nil {
		return nil, errors.New(
			"recursive: no Sizer, so there is no unit to count a chunk in; " +
				"pass Characters{} for chonkie's character tokenizer, or a " +
				"*chonkie.Tokenizer for the vocabulary EMBEDDING_TOKENIZER names")
	}
	return c.cut(text, 0)
}

// cut is chonkie's `_recursive_chunk`.
func (c Chunker) cut(text string, depth int) ([]string, error) {
	if text == "" {
		return nil, nil
	}
	if depth >= len(defaultLevels) {
		// Past the last level there is nothing left to try, so the piece
		// stands however long it is. chonkie does the same.
		return []string{text}, nil
	}
	current := defaultLevels[depth]

	pieces, err := c.splitText(text, current)
	if err != nil {
		return nil, err
	}
	counts := make([]int, len(pieces))
	for i, piece := range pieces {
		if counts[i], err = c.Sizer.Count(piece); err != nil {
			return nil, fmt.Errorf("recursive: counting a level-%d piece: %w", depth, err)
		}
	}

	// The token level's pieces are already cut to the ceiling, so chonkie
	// leaves them as they are rather than merging them back together.
	merged, mergedCounts := pieces, counts
	if current.cutsAtText() {
		if merged, mergedCounts, err = chonkie.MergeSplits(pieces, counts, c.Size); err != nil {
			return nil, fmt.Errorf("recursive: merging level-%d pieces: %w", depth, err)
		}
	}

	var chunks []string
	for i, piece := range merged {
		// mergedCounts[i], not a fresh count of piece. The core's count for a
		// merged piece is the sum of the counts it joined, and with a real
		// vocabulary that is not what re-counting the joined text gives.
		// chonkie branches on the sum, so branching on anything else would
		// recurse where chonkie does not, and cut where chonkie does not.
		if mergedCounts[i] > c.Size {
			deeper, err := c.cut(piece, depth+1)
			if err != nil {
				return nil, err
			}
			chunks = append(chunks, deeper...)
			continue
		}
		chunks = append(chunks, piece)
	}
	return chunks, nil
}

// splitText is chonkie's `_split_text`: the core's cuts at a level that cuts
// at text, or the Sizer's cuts at the token level.
func (c Chunker) splitText(text string, current level) ([]string, error) {
	if !current.cutsAtText() {
		return c.cutAtTokens(text)
	}
	spans, err := current.splitSpans(text)
	if err != nil {
		return nil, err
	}
	pieces := make([]string, 0, len(spans))
	for _, span := range spans {
		// chonkie drops empty pieces before counting them. The core does not
		// produce any, but the filter is chonkie's, so it is kept.
		if piece := text[span.Start:span.End]; piece != "" {
			pieces = append(pieces, piece)
		}
	}
	return pieces, nil
}

// cutAtTokens is the last level: slice the text at the token boundaries the
// Sizer reports, rather than decoding token ids back into text.
//
// The cut points are checked before they are used. A Sizer is an interface,
// and a cut point outside the text or out of order would slice a string in
// the wrong place -- which is the one failure this package exists to prevent,
// so it is refused rather than trusted.
func (c Chunker) cutAtTokens(text string) ([]string, error) {
	cuts, err := c.Sizer.SplitOffsets(text, c.Size)
	if err != nil {
		return nil, fmt.Errorf("recursive: cutting at token boundaries: %w", err)
	}
	if len(cuts) == 0 {
		return nil, errors.New("recursive: the Sizer returned no cut points at all")
	}
	if cuts[0] != 0 || cuts[len(cuts)-1] != len(text) {
		return nil, fmt.Errorf(
			"recursive: cut points run %d..%d, so they do not cover the whole %d-byte text",
			cuts[0], cuts[len(cuts)-1], len(text))
	}
	pieces := make([]string, 0, len(cuts)-1)
	for i := 1; i < len(cuts); i++ {
		if cuts[i] <= cuts[i-1] {
			return nil, fmt.Errorf(
				"recursive: cut point %d (%d) does not come after cut point %d (%d)",
				i, cuts[i], i-1, cuts[i-1])
		}
		pieces = append(pieces, text[cuts[i-1]:cuts[i]])
	}
	return pieces, nil
}
