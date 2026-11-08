// Package tablesafe chunks a document without cutting a Markdown table in
// half: each table block is emitted whole and the prose between the tables is
// chunked around it.
//
// It is `chunk_text_hybrid` in
// soloclef_backend/app/indexing_pipeline/document_chunker.py, which is what
// the indexing pipeline actually calls, so its boundaries -- including the
// whitespace it strips off every piece -- are the ones in the database.
package tablesafe

import "git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"

// Chunks returns the chunk texts in document order.
//
// Unlike the recursive chunker's, these do not reconstruct the input: the
// whitespace around each prose segment and each table is stripped, so a
// document's chunks can be shorter than the document.
func Chunks(text string, size int) []string {
	chunker := recursive.Chunker{Size: size}
	var chunks []string
	cursor := 0
	for _, block := range tableBlocks(text) {
		if prose := trimPythonWhitespace(text[cursor:block.start]); prose != "" {
			chunks = append(chunks, chunker.Chunks(prose)...)
		}
		if table := trimPythonWhitespace(text[block.start:block.end]); table != "" {
			chunks = append(chunks, table)
		}
		cursor = block.end
	}
	if trailing := trimPythonWhitespace(text[cursor:]); trailing != "" {
		chunks = append(chunks, chunker.Chunks(trailing)...)
	}
	return chunks
}
