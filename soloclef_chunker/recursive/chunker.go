// Package recursive reproduces chonkie's RecursiveChunker, as this app
// configures it, exactly: the same cuts in the same places, byte for byte.
//
// The boundaries it decides are already in the database against the chunks
// and vectors they produced, so this is not an implementation of recursive
// chunking -- it is a reproduction of one, and a cut that lands one character
// from chonkie's is a corpus that no longer matches its index.
package recursive

// DefaultChunkSize is the ceiling the app runs with and the one its stored
// chunks were cut to: 512 of whatever unit sizes a chunk, which is characters
// while no tokenizer is named (soloclef_backend/app/config/chunk_sizing.py).
const DefaultChunkSize = 512

// Chunker cuts text into pieces of at most Size characters.
type Chunker struct {
	Size int
}

// Chunks returns the chunk texts in document order. Their concatenation is
// the input: no level drops or rewrites a character.
func (c Chunker) Chunks(text string) []string {
	return c.cut(text, 0)
}

func (c Chunker) cut(text string, depth int) []string {
	if text == "" {
		return nil
	}
	if depth >= len(defaultLevels) {
		return []string{text}
	}
	current := defaultLevels[depth]

	var pieces []string
	if current.cutsAtDelimiters() {
		pieces = splitAtDelimiters(text, current.delimiters)
	} else {
		pieces = splitEveryNCharacters(text, c.Size)
	}
	pieces = withoutEmpty(pieces)

	counts := make([]int, len(pieces))
	for i, piece := range pieces {
		counts[i] = characterCount(piece)
	}

	// The last level's pieces are already cut to the ceiling, so chonkie
	// leaves them as they are rather than merging them back together.
	merged := pieces
	if current.cutsAtDelimiters() {
		merged = mergeIntoChunkSize(pieces, counts, c.Size)
	}

	var chunks []string
	for _, piece := range merged {
		if characterCount(piece) > c.Size {
			chunks = append(chunks, c.cut(piece, depth+1)...)
			continue
		}
		chunks = append(chunks, piece)
	}
	return chunks
}

func withoutEmpty(pieces []string) []string {
	out := pieces[:0]
	for _, piece := range pieces {
		if piece != "" {
			out = append(out, piece)
		}
	}
	return out
}
