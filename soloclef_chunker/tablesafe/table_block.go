package tablesafe

import "strings"

// A Markdown table block: one or more consecutive lines that begin, after
// optional spaces and tabs, with a pipe, each ending in a newline.
type tableBlock struct {
	start, end int
}

// tableBlocks finds the blocks in document order, reproducing what
// `re.finditer` returns for the pattern
// `(?:(?:^|\n)(?=[ \t]*\|)(?:[ \t]*\|[^\n]*\n)+)` under re.MULTILINE.
//
// Written out by hand because the pattern needs a lookahead and alternation
// order that Go's RE2 engine does not offer. Two details of the pattern
// decide where a block starts and both are kept: the `^` alternative is tried
// before the `\n` one, so a block that already starts a line does not swallow
// the newline before it; and a row must end in a newline, so a table that
// runs to the end of the document without one is not a block at all and is
// chunked as prose.
func tableBlocks(text string) []tableBlock {
	var blocks []tableBlock
	for position := 0; position <= len(text); {
		end, found := tableBlockAt(text, position)
		if !found {
			position++
			continue
		}
		blocks = append(blocks, tableBlock{start: position, end: end})
		position = end
	}
	return blocks
}

func tableBlockAt(text string, position int) (int, bool) {
	if position == 0 || (position > 0 && text[position-1] == '\n') {
		if end, found := tableRows(text, position); found {
			return end, true
		}
	}
	if position < len(text) && text[position] == '\n' {
		if end, found := tableRows(text, position+1); found {
			return end, true
		}
	}
	return 0, false
}

func tableRows(text string, position int) (int, bool) {
	rows := 0
	for {
		pipe := position
		for pipe < len(text) && (text[pipe] == ' ' || text[pipe] == '\t') {
			pipe++
		}
		if pipe >= len(text) || text[pipe] != '|' {
			break
		}
		newline := strings.IndexByte(text[pipe:], '\n')
		if newline < 0 {
			break
		}
		position = pipe + newline + 1
		rows++
	}
	return position, rows > 0
}
