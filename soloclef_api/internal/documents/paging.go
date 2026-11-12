package documents

import "math"

// chunkPaging is get_document_chunks_paginated's three query parameters,
// after FastAPI's bounds: `page` at least 0, `page_size` from 1 to 100, and
// `start_offset` at least 0 when it is given at all.
type chunkPaging struct {
	page     number
	pageSize int64
	// startOffset is nil when the request did not name it. Zero is a value:
	// `start_offset=0&page=2` reads from the start, which is the Python
	// expression's `is not None` and not a truthiness test.
	startOffset *number
}

// offset is `start_offset if start_offset is not None else page * page_size`.
//
// It reports false when the result is past int64. Python forms the product
// whatever its size and the *statement* fails to encode it, so that is where
// the failure has to be reported here -- after the document was looked for,
// and after the count, which is issued first. An int64 would
// wrap instead and answer 200 about another page
// (soloclef_api/AGENTS.md, "Do not multiply two numbers a Python route
// multiplies").
//
// A `page` too wide to multiply is not a failure when `start_offset` is
// given: the expression never reads it.
func (p chunkPaging) offset() (int64, bool) {
	if p.startOffset != nil {
		if p.startOffset.tooWide() {
			return 0, false
		}
		return p.startOffset.value, true
	}
	if p.page.tooWide() {
		return 0, false
	}
	// Both factors are non-negative and the page size is at least 1, so the
	// product overflows exactly when the page is past this quotient.
	if p.page.value > math.MaxInt64/p.pageSize {
		return 0, false
	}
	return p.page.value * p.pageSize, true
}

// reportedPage is `offset // page_size if page_size else page`.
//
// The page size is never zero -- FastAPI refuses one below 1 -- so the second
// branch cannot run and the answer is always worked out from the offset. That
// is why a request for `start_offset=7&page=0&page_size=5` is told it is on
// page 1. Both operands are non-negative, so Go's division is Python's floor
// division here.
func (p chunkPaging) reportedPage(offset int64) int64 {
	return offset / p.pageSize
}

// hasMore is `(offset + len(chunks)) < total`, written as the subtraction
// that cannot overflow: an offset of int64's maximum is a request a client
// can make, and it is an empty page with nothing after it.
func hasMore(offset int64, returned int, total int64) bool {
	return offset < total-int64(returned)
}
