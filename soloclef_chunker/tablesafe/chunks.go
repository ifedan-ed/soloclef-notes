// Package tablesafe chunks a document without cutting a Markdown table row in
// half: the tables are recognised first and packed row by row, each chunk
// carrying the table's header above its rows, and the prose between them goes
// to the recursive chunker.
//
// It is `chunk_text_hybrid` in the Python app's
// app/indexing_pipeline/document_chunker.py, which is what its indexing
// pipeline called, so its boundaries are the ones in the database.
//
// What changed on 2026-11-10, on both sides
// -----------------------------------------
// A table used to be emitted as one indivisible chunk however large it was.
// Measured on the live chunker: 18,190 characters of table (400 rows) became
// a single 18,136-character chunk, about 5,200 BGE-M3 tokens against a
// 512-token model window. One vector for 400 patients retrieves none of them,
// and past about 21,000 characters the pre-embedding truncation cut the tail
// off instead, so rows were not indexed and nothing said so.
//
// Now a table is packed row by row up to the chunk budget. Each chunk after
// the first repeats the table's header -- "3.5 | 12 | negative" means nothing
// without the column headings -- and carries the table's labelled caption and
// notes while they fit. A table chunk is therefore NOT always a verbatim
// slice of the document, which is new: the prose chunks still are.
//
// Where a chunk came from
// ------------------------
// Pieces returns each chunk with the span of the document its own content was
// cut from. A citation's line range and page range are arithmetic on that
// span (count the newlines before it; look it up in the page table), so
// nothing has to search the document for the chunk afterwards -- which could
// not work for a table chunk anyway, since it begins with a header copied
// from elsewhere. The span is of the rows, never of the synthesised header,
// caption or notes: that is Python's `TableAwareChunk.raw_start`/`raw_end`.
//
// What is not ported: which compromise a table chunk had to make
// (`TableAwareChunk.quality`). It never reaches a chunk's text or position.
package tablesafe

import (
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
)

// OversizeFactor is how much bigger than the ordinary chunk budget a table
// chunk is ever allowed to be.
//
// A table row is indivisible -- cutting inside one is the thing this package
// exists to prevent -- so a wide row has to be allowed to overflow the budget
// rather than be split. Four times the budget is the ceiling past which the
// row is split anyway. At the configured 512 BGE-M3 tokens that is 2,048,
// still well inside the 6,000-token pre-embedding ceiling, so a chunk at this
// ceiling still reaches the embedder whole.
const OversizeFactor = 4

// Chunker cuts a document into the chunks that get embedded and stored.
//
// Size and Sizer mean what they mean in recursive.Chunker, and are required
// for the same reason: the vocabulary decides every boundary, including a
// table's, because whether one more row still fits is a question about
// tokens.
type Chunker struct {
	Size  int
	Sizer recursive.Sizer
}

// Piece is one chunk and where in the document it was cut from.
type Piece struct {
	// Text is the chunk: what is embedded and stored.
	Text string
	// Start and End bound the part of the document the chunk's own content
	// is: document[Start:End]. For a prose chunk that slice is Text exactly.
	// For a table chunk it is the rows the chunk holds, and Text may carry a
	// header, a caption and notes around them that sit elsewhere in the
	// document (or, for the first chunk of a table, the header in place).
	Start, End int
}

// Chunks returns the chunk texts in document order.
//
// Unlike the recursive chunker's, these do not reconstruct the input. Blank
// pieces are dropped, and a table chunk may carry a header, caption or notes
// repeated from elsewhere in the document, so the chunks of a document can be
// both shorter and longer than the document.
func (c Chunker) Chunks(text string) ([]string, error) {
	pieces, err := c.Pieces(text)
	if err != nil {
		return nil, err
	}
	// nil for no pieces, as before Pieces existed: callers compare with it.
	var chunks []string
	for _, piece := range pieces {
		chunks = append(chunks, piece.Text)
	}
	return chunks, nil
}

// Pieces returns the chunks in document order, each with the span of the
// document it was cut from. The texts are exactly what Chunks returns: this
// is the one implementation, and Chunks drops the spans.
func (c Chunker) Pieces(text string) ([]Piece, error) {
	if c.Size < 1 {
		return nil, fmt.Errorf("tablesafe: chunk size must be at least 1, got %d", c.Size)
	}
	if c.Sizer == nil {
		return nil, fmt.Errorf(
			"tablesafe: no Sizer, so there is no unit to count a chunk in; " +
				"pass the same one the recursive chunker is given")
	}
	budget := c.Size
	limit := OversizeFactor * budget

	var chunks []Piece
	cursor := 0
	for _, table := range findMarkdownTables(text) {
		if err := c.emitProse(&chunks, text, cursor, table.start); err != nil {
			return nil, err
		}
		if err := c.emitTable(&chunks, text, table, budget, limit); err != nil {
			return nil, err
		}
		cursor = table.end
	}
	if err := c.emitProse(&chunks, text, cursor, len(text)); err != nil {
		return nil, err
	}
	return chunks, nil
}

// count is a text's size in the unit chunk budgets are expressed in: the same
// Sizer the prose chunker counts with, so a table chunk's budget and a prose
// chunk's budget mean the same thing.
func (c Chunker) count(text string) (int, error) {
	n, err := c.Sizer.Count(text)
	if err != nil {
		return 0, fmt.Errorf("tablesafe: counting a candidate chunk: %w", err)
	}
	return n, nil
}

// emitProse chunks the ordinary prose in [start, end) and appends it.
//
// The segment is handed to the recursive chunker whole, NOT stripped first.
// Python strips nothing here either: it tests whether the segment is blank,
// and then drops whichever individual pieces come back blank. A piece that
// survives keeps the whitespace it was cut with, which is why a prose chunk
// can begin or end on a space.
func (c Chunker) emitProse(chunks *[]Piece, text string, start, end int) error {
	if end <= start {
		return nil
	}
	segment := text[start:end]
	if isBlank(segment) {
		return nil
	}
	pieces, err := recursive.Chunker{Size: c.Size, Sizer: c.Sizer}.Chunks(segment)
	if err != nil {
		return fmt.Errorf("tablesafe: chunking the prose at %d..%d: %w", start, end, err)
	}
	appendSlices(chunks, pieces, start)
	return nil
}

// appendSlices appends the recursive chunker's pieces of one segment, which
// began at `offset` in the document, dropping the blank ones.
//
// The recursive chunker slices its input and drops nothing (its pieces
// concatenate to the segment; recursive.Chunker.Chunks says so and its tests
// hold it to that), so each piece's place is the running total of the lengths
// before it. A blank piece is skipped but still counted.
func appendSlices(chunks *[]Piece, pieces []string, offset int) {
	for _, piece := range pieces {
		end := offset + len(piece)
		if !isBlank(piece) {
			*chunks = append(*chunks, Piece{Text: piece, Start: offset, End: end})
		}
		offset = end
	}
}

// emitTable packs one table's rows into chunks, each headed and in order.
func (c Chunker) emitTable(chunks *[]Piece, text string, table markdownTable, budget, limit int) error {
	header := text[table.start:table.headerEnd]
	caption := ""
	if table.contextBefore != "" {
		caption = table.contextBefore + "\n\n"
	}
	suffix := ""
	if table.contextAfter != "" {
		suffix = "\n\n" + table.contextAfter
	}

	// emit writes one chunk for the whole rows in [start, end).
	//
	// The rows themselves are never cut here. When the chunk will not fit,
	// the SYNTHESISED context is given up instead, in order of how little it
	// costs to lose: first the caption and notes, then the repeated header.
	// Only if dropping both still leaves it over the hard ceiling is the span
	// cut by the ordinary chunker, which is the one case where a chunk's
	// cells may not line up with its headings.
	emit := func(start, end int) error {
		raw := text[start:end]
		// The first chunk starts at the table's own header, so it needs no
		// repeated one.
		repeated := header
		if start == table.start {
			repeated = ""
		}
		for _, attempt := range []struct{ prefix, tail string }{
			{caption + repeated, suffix},
			{repeated, ""},
			{"", ""},
		} {
			body := attempt.prefix + raw + attempt.tail
			size, err := c.count(body)
			if err != nil {
				return err
			}
			if size <= limit {
				// The span is of the rows, whatever was put around them.
				*chunks = append(*chunks, Piece{Text: body, Start: start, End: end})
				return nil
			}
		}
		return c.emitSplitRow(chunks, raw, start)
	}

	// [start, end) is the span waiting to be emitted: the header (on the
	// first chunk only) followed by whole rows. It holds at least one row
	// when end > max(start, table.headerEnd).
	start := table.start
	end := table.headerEnd
	for _, row := range table.rows {
		rowText := text[row.start:row.end]
		rowAlone, err := c.count(caption + header + rowText + suffix)
		if err != nil {
			return err
		}
		if rowAlone > limit {
			// A pathological row -- a merged cell holding a paragraph, or a
			// header wider than the ceiling on its own. Index it degraded
			// rather than failing the whole document.
			if end > max(start, table.headerEnd) {
				if err := emit(start, end); err != nil {
					return err
				}
				start = row.start
			}
			if start == table.start {
				// Only counted when the pending span still begins at the
				// table's own header: Python's `and` short-circuits here, and
				// counting anyway would be wasted work on every wide row.
				withHeader, err := c.count(header + rowText)
				if err != nil {
					return err
				}
				if withHeader > limit {
					// The real header cannot share a chunk with this row
					// either.
					if err := emit(table.start, table.headerEnd); err != nil {
						return err
					}
					start = row.start
				}
			}
			if err := emit(start, row.end); err != nil {
				return err
			}
			start, end = row.end, row.end
			continue
		}

		prefix := caption
		if start != table.start {
			prefix += header
		}
		if end > max(start, table.headerEnd) {
			pending, err := c.count(prefix + text[start:row.end] + suffix)
			if err != nil {
				return err
			}
			if pending > budget {
				// Adding this row would push the pending chunk over budget,
				// so close it on the previous row boundary and begin a new
				// one here.
				if err := emit(start, end); err != nil {
					return err
				}
				start = row.start
			}
		}
		end = row.end
	}

	if end > start {
		return emit(start, end)
	}
	return nil
}

// emitSplitRow is the last resort: a span of table that will not fit even
// with every piece of synthesised context dropped, cut by the ordinary
// chunker. Its chunks are cells without headings, which is why Python records
// it as the one degraded quality worth a warning.
func (c Chunker) emitSplitRow(chunks *[]Piece, raw string, offset int) error {
	pieces, err := recursive.Chunker{Size: c.Size, Sizer: c.Sizer}.Chunks(raw)
	if err != nil {
		return fmt.Errorf("tablesafe: cutting a row too wide to index whole: %w", err)
	}
	appendSlices(chunks, pieces, offset)
	return nil
}
