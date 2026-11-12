package tablesafe

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Conservative recognition of Markdown pipe tables, with their caption and
// notes. It is the Python app's app/indexing_pipeline/markdown_tables.py,
// ported function for function.
//
// What it replaced, on both sides, was one loose regular expression:
//
//	(?:(?:^|\n)(?=[ \t]*\|)(?:[ \t]*\|[^\n]*\n)+)
//
// which matches any run of lines beginning with a pipe. Measured against the
// live chunker, that was four wrong answers: a fenced code block of shell
// pipelines torn out of its prose with the fences landing in different chunks
// from the code; a table at end-of-file with no trailing newline losing its
// last row, which then became a chunk of cells with no header; pipe-leading
// prose indexed as a table; and no width check, so two adjacent tables merged
// into one.
//
// A table here is a header line, a |---|---| separator with the SAME number
// of cells, and at least one body row of that same width, outside any code
// fence. Nothing is inferred -- not a missing cell, not a header continued
// from a previous page, not a merged cell.
//
// Every offset is a byte index into the original text, so the caller slices
// the source with them directly.

// markdownTable is one recognised pipe table, located in the text it was
// found in.
//
// start..headerEnd is the header line plus the separator line: the part that
// has to be repeated above any chunk of rows that does not already begin with
// it. start..end is the whole table.
//
// rows holds one span per body row, in order and contiguous, so a caller can
// pack whole rows into chunks without ever cutting inside one.
//
// contextStart..contextEnd widen the span to take in an adjacent caption and
// notes paragraph, when -- and only when -- those paragraphs are explicitly
// labelled as such.
type markdownTable struct {
	start     int
	headerEnd int
	end       int
	rows      []span

	contextStart int
	contextEnd   int
	// The labelled caption/units paragraph(s) above the table, and the
	// labelled notes paragraph(s) below it, already joined. Empty when the
	// neighbouring text was ordinary prose.
	contextBefore string
	contextAfter  string
}

// span is a half-open byte range [start, end) into the document.
type span struct {
	start, end int
}

// The character classes Python's `re` gives a str pattern, written out.
//
// Go's regexp is ASCII where Python's is Unicode: Go's \s is [\t\n\f\r ] and
// Go's \w and \b are ASCII-only. Using them would match a caption Python does
// not match, or miss one Python matches, and either moves a table's span.
const (
	// Python's \s. The same set isPythonSpace holds; it is repeated here
	// because a regexp needs it as a class and a function cannot be one.
	pySpace = `\t\n\v\f\r \x{1c}-\x{1f}\x{85}\x{a0}\x{1680}` +
		`\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	// Python's \w: alphanumeric or underscore, by Unicode category.
	pyWord = `\p{L}\p{N}_`
	// Python's \b after a word character, at the end of a prefix match.
	// Go's \b would call the boundary between "Table" and "é" a boundary
	// (é is not an ASCII word character) where Python does not. Consuming
	// the following character instead is equivalent for a prefix match,
	// which is all .match() ever asks.
	pyWordEnd = `(?:$|[^` + pyWord + `])`
)

// A paragraph that may be repeated with the rows it belongs to: one that says
// it is a caption, a table number, or a units line. Anything else above a
// table stays independently retrievable prose and is never duplicated.
var captionPattern = regexp.MustCompile(
	`(?i)^[#*~` + pySpace + `]*(?:table` + pyWordEnd + `|caption` + pyWordEnd +
		`|units?[` + pySpace + `]*[:(])`)

// A paragraph starting "Table ..." below a table introduces the NEXT table,
// so it is never swept up as this one's notes.
var nextTablePattern = regexp.MustCompile(`(?i)^[#*~` + pySpace + `]*table` + pyWordEnd)

// A paragraph below a table that belongs to it: a numbered footnote
// reference, or a labelled notes/units/source line.
//
// The footnote-marker alternative -- `[*†‡](?![*])` -- is not here. It needs a
// negative lookahead and Go's RE2 engine has none by design, so it is the
// hand-written half of notesParagraph below.
var notesPattern = regexp.MustCompile(
	`(?i)^(?:\[\^?[` + pyWord + `]+\]|[*~]*(?:notes?` + pyWordEnd +
		`|footnotes?` + pyWordEnd + `|units?` + pyWordEnd +
		`|source[` + pySpace + `]*:))`)

// isNotesParagraph is Python's _NOTES.match(paragraph) as a whole, including
// the alternative RE2 cannot express.
//
// A single *, dagger or double dagger opens a footnote; "**" opens bold text
// and is not one, which is what the lookahead in the Python pattern says. The
// alternatives are independent, so trying the hand-written one first and the
// compiled ones after gives the same answer as one pattern would.
func isNotesParagraph(paragraph string) bool {
	if marker, width := utf8.DecodeRuneInString(paragraph); marker == '*' || marker == '†' || marker == '‡' {
		next, _ := utf8.DecodeRuneInString(paragraph[width:])
		if next != '*' {
			return true
		}
	}
	return notesPattern.MatchString(paragraph)
}

// splitCells is the cells of one pipe-table line, stripped, outer empties
// dropped.
//
// A pipe preceded by an odd number of backslashes is escaped and is part of a
// cell's text; an even number (including none) means the pipe is a cell
// boundary. `|a\|b|c|` is therefore two cells, not three.
//
// The scan is over bytes rather than runes, which is safe and deliberate:
// '|' and '\\' are ASCII, and no byte of a multi-byte UTF-8 character is ever
// below 0x80, so a continuation byte can neither be mistaken for a pipe nor
// break the backslash count.
func splitCells(line string) []string {
	var cells []string
	start := 0
	backslashes := 0
	for i := 0; i < len(line); i++ {
		char := line[i]
		if char == '|' && backslashes%2 == 0 {
			cells = append(cells, strip(line[start:i]))
			start = i + 1
		}
		if char == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
	}
	if len(cells) == 0 {
		// No unescaped pipe at all: not a table line.
		return nil
	}
	cells = append(cells, strip(line[start:]))
	// A conventional table line is written `| a | b |`, which yields an empty
	// cell at each end. Drop those, but only if they are empty.
	if cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// isSeparatorRow reports whether every cell is a separator cell: `---`,
// `:---`, `---:` or `:---:`, which is Python's `_SEPARATOR_CELL.fullmatch`
// over all of them.
func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if !isSeparatorCell(cell) {
			return false
		}
	}
	return true
}

// isSeparatorCell is `:?-+:?` matched against the whole cell.
func isSeparatorCell(cell string) bool {
	cell = strings.TrimPrefix(cell, ":")
	cell = strings.TrimSuffix(cell, ":")
	if cell == "" {
		return false
	}
	for i := 0; i < len(cell); i++ {
		if cell[i] != '-' {
			return false
		}
	}
	return true
}

// fenceRun is the opening or closing run of a fenced code block: three or
// more backticks or tildes at the start of the line, or "" for anything else.
// Anything inside a fence is prose as far as this file cares.
func fenceRun(stripped string) string {
	if stripped == "" {
		return ""
	}
	marker := stripped[0]
	if marker != '`' && marker != '~' {
		return ""
	}
	length := 0
	for length < len(stripped) && stripped[length] == marker {
		length++
	}
	if length < 3 {
		return ""
	}
	return stripped[:length]
}

// findMarkdownTables is every pipe table in text, in document order,
// non-overlapping.
//
// A run of lines is a table only if all of these hold:
//
//   - it is not inside a fenced code block, and not an indented code block
//     (four spaces or a tab);
//   - the first line has at least one cell;
//   - the second line is a separator with exactly as many cells;
//   - at least one further line has exactly as many cells.
//
// A line that begins a new header/separator pair ends the current table even
// without a blank line between them, so two adjacent tables stay separate.
// A table may end at end-of-file without a trailing newline.
func findMarkdownTables(text string) []markdownTable {
	lines := splitLinesKeepingEnds(text)
	// offsets[i] is where line i starts; offsets[len(lines)] is len(text).
	offsets := make([]int, len(lines)+1)
	for i, line := range lines {
		offsets[i+1] = offsets[i] + len(line)
	}

	var tables []markdownTable
	fence := ""
	for i := 0; i < len(lines)-1; {
		stripped := strip(lines[i])
		indented := hasPythonIndent(lines[i])

		if run := fenceRun(stripped); run != "" && !indented {
			switch {
			case fence == "":
				fence = run
			case run[0] == fence[0] && len(run) >= len(fence) && stripped == run:
				// A closing fence must be the whole line and at least as long
				// as the one that opened it, per CommonMark.
				fence = ""
			}
			i++
			continue
		}
		if fence != "" || indented {
			i++
			continue
		}

		header := splitCells(stripped)
		separator := splitCells(strip(lines[i+1]))
		if len(header) == 0 || len(header) != len(separator) || !isSeparatorRow(separator) {
			i++
			continue
		}

		// Body rows: same width, not a fence, not the start of a new table.
		end := i + 2
		for end < len(lines) && len(splitCells(strip(lines[end]))) == len(header) {
			if fenceRun(strip(lines[end])) != "" {
				break
			}
			if end+1 < len(lines) {
				following := splitCells(strip(lines[end+1]))
				if len(following) == len(header) && isSeparatorRow(following) {
					break
				}
			}
			end++
		}
		if end == i+2 {
			// Header and separator with no body: not worth calling a table.
			i++
			continue
		}

		captions, contextStart := captionsAbove(lines, i)
		notes, contextEnd := notesBelow(lines, end)

		rows := make([]span, 0, end-(i+2))
		for r := i + 2; r < end; r++ {
			rows = append(rows, span{start: offsets[r], end: offsets[r+1]})
		}
		tables = append(tables, markdownTable{
			start:         offsets[i],
			headerEnd:     offsets[i+2],
			end:           offsets[end],
			rows:          rows,
			contextStart:  offsets[contextStart],
			contextEnd:    offsets[contextEnd],
			contextBefore: strings.Join(captions, "\n\n"),
			contextAfter:  strings.Join(notes, "\n\n"),
		})
		i = end
	}
	return tables
}

// captionsAbove is the labelled caption/units paragraphs immediately above
// the table, nearest last, and the line the first of them starts on.
func captionsAbove(lines []string, tableLine int) ([]string, int) {
	var captions []string
	contextStart := tableLine
	before := tableLine
	for before > 0 {
		for before > 0 && isBlank(lines[before-1]) {
			before--
		}
		first := before
		for first > 0 && !isBlank(lines[first-1]) && !strings.Contains(lines[first-1], "|") {
			first--
		}
		paragraph := strip(strings.Join(lines[first:before], ""))
		if !captionPattern.MatchString(paragraph) {
			break
		}
		captions = append([]string{paragraph}, captions...)
		contextStart = first
		before = first
	}
	return captions, contextStart
}

// notesBelow is the labelled notes/footnote paragraphs immediately below the
// table, and the line after the last of them.
func notesBelow(lines []string, tableEnd int) ([]string, int) {
	var notes []string
	contextEnd := tableEnd
	after := tableEnd
	for after < len(lines) {
		for after < len(lines) && isBlank(lines[after]) {
			after++
		}
		last := after
		for last < len(lines) && !isBlank(lines[last]) && !strings.Contains(lines[last], "|") {
			last++
		}
		paragraph := strip(strings.Join(lines[after:last], ""))
		if nextTablePattern.MatchString(paragraph) || !isNotesParagraph(paragraph) {
			break
		}
		notes = append(notes, paragraph)
		contextEnd = last
		after = last
	}
	return notes, contextEnd
}
