package indexing

import "git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"

// plan says, for a document's new chunks, which reuse a row the document
// already has and which are new.
type plan struct {
	// kept maps a new chunk's index to the id of the existing row it reuses.
	kept map[int]int64
	// fresh lists the indexes of the chunks that have to be embedded and
	// inserted, in document order.
	fresh []int
}

// reconcile matches a document's new chunks against the rows it has.
//
// A chunk whose text is byte-for-byte the text of an existing row keeps that
// row, and with it the vector already in Qdrant; only its place is updated.
// So an edit to one paragraph of a long note embeds a chunk or two instead
// of the note, and a job run a second time embeds nothing.
//
// A row is only kept when its vector is known to be in Qdrant (`present`). A
// row kept without one would be found by keyword search and never by meaning
// search, with nothing to say so; such a chunk is treated as new.
//
// Rows are matched by text alone, and each row is used once. When a text
// occurs several times (a repeated table header, a boilerplate footer) the
// rows are handed out in their old order, which keeps each as near as it can
// be to where it was.
func reconcile(existing []indexstore.ExistingChunk, present map[int64]bool, pieces []Piece) plan {
	available := make(map[string][]int64, len(existing))
	for _, chunk := range existing {
		if present[chunk.ID] {
			available[chunk.Content] = append(available[chunk.Content], chunk.ID)
		}
	}
	result := plan{kept: map[int]int64{}}
	for index, piece := range pieces {
		if ids := available[piece.Text]; len(ids) > 0 {
			result.kept[index] = ids[0]
			available[piece.Text] = ids[1:]
			continue
		}
		result.fresh = append(result.fresh, index)
	}
	return result
}
