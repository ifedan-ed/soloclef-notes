package documents

// windowBounds is get_document_by_chunk_id's
//
//	start = max(0, cited_idx - chunk_window)
//	end = min(total_chunks, cited_idx + chunk_window + 1)
//
// for a window that may be wider than an int64. Python's integers are
// unbounded and neither expression reaches a statement until `max` and `min`
// have brought it back inside the document, so a window of twenty digits is
// simply every chunk: `start` is 0 and `end` is the total. Written with the
// same two additions in int64 it would overflow instead -- at a window of
// int64's own maximum, which a client can send -- and answer about a
// different part of the document.
//
// So neither sum is formed. Each is replaced by the comparison that decides
// it, on the side that cannot overflow.
func windowBounds(total, before int64, window number) (start, end int64) {
	// The route refuses a negative window with a 422 before this is reached,
	// so `beyond` is never negative here and a wide window is a large one.
	if window.tooWide() {
		return 0, total
	}
	// before - window, when that is positive.
	if window.value < before {
		start = before - window.value
	}
	// before + window + 1 <= total  <=>  window <= total - before - 1, whose
	// right-hand side cannot overflow: both counts are between 0 and int64's
	// maximum.
	if window.value <= total-before-1 {
		end = before + window.value + 1
	} else {
		end = total
	}
	return start, end
}
