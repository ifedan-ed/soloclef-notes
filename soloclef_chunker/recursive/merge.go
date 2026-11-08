package recursive

import "strings"

// mergeIntoChunkSize joins consecutive pieces while they fit, as
// chonkie-core's merge_splits does: a piece is added to the one being built
// unless that would carry it past size, and a piece that is already longer
// than size stands alone rather than pulling its neighbour in with it.
func mergeIntoChunkSize(pieces []string, counts []int, size int) []string {
	var out []string
	var current strings.Builder
	running := 0
	for i, piece := range pieces {
		if running > 0 && running+counts[i] > size {
			out = append(out, current.String())
			current.Reset()
			running = 0
		}
		current.WriteString(piece)
		running += counts[i]
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}
