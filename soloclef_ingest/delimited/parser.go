// Package delimited turns a CSV or TSV file into one Markdown table, which
// the table-aware chunker then splits by rows with the header repeated.
package delimited

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/utf8text"
)

// Parser reads CSV and TSV files up to MaxBytes long.
type Parser struct {
	maxBytes int64
}

// New returns a Parser. maxBytes is required: see utf8text.Read.
func New(maxBytes int64) (*Parser, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("delimited: maxBytes must be at least 1, got %d", maxBytes)
	}
	return &Parser{maxBytes: maxBytes}, nil
}

func (*Parser) ID() parse.ID { return parse.Builtin }

func (*Parser) Reads(extension string) bool {
	kind := filekind.Of(extension)
	return kind == filekind.CommaSeparated || kind == filekind.TabSeparated
}

func (p *Parser) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	kind := filekind.Of(filekind.Extension(source.Name))
	if kind != filekind.CommaSeparated && kind != filekind.TabSeparated {
		return parse.Document{}, &parse.Error{
			Kind: parse.UnsupportedFormat, Parser: parse.Builtin,
			Message: fmt.Sprintf("%s is not a CSV or TSV file", source.Name),
		}
	}
	text, err := utf8text.Read(ctx, source, p.maxBytes)
	if err != nil {
		return parse.Document{}, err
	}
	table, err := markdownTable(text, kind == filekind.TabSeparated)
	if err != nil {
		return parse.Document{}, err
	}
	return parse.Unpaged(parse.Builtin, table)
}

func reader(text string, tabs bool) *csv.Reader {
	records := csv.NewReader(strings.NewReader(text))
	// Rows of different lengths are ordinary in exported data; the table is
	// made as wide as the widest (see markdownTable), not rejected.
	records.FieldsPerRecord = -1
	// The slice is reused between rows: each row is written out before the
	// next is read, so nothing keeps it.
	records.ReuseRecord = true
	if tabs {
		records.Comma = '\t'
		// TSV has no quoting convention: a field that happens to start with
		// a quote is data. CSV stays strict, so a file with a broken quote
		// is refused with its line number instead of having the rest of the
		// file swallowed into one cell.
		records.LazyQuotes = true
	}
	return records
}

// markdownTable reads the text twice. The first pass only measures: how wide
// the widest row is. A Markdown table's width is set by its header, and a
// renderer drops the cells of a longer row, so the width has to be known
// before the header is written. (The Python tier cut every row to the
// header's width, losing those cells.) Reading twice is cheaper than keeping
// every record: the second pass writes straight into the output.
func markdownTable(text string, tabs bool) (string, error) {
	width := 0
	measure := reader(text, tabs)
	for {
		record, err := measure.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", malformed(err)
		}
		width = max(width, len(record))
	}
	if width == 0 {
		return "", nil
	}

	var table strings.Builder
	// Each cell gains " | " and the text is otherwise the same size, so this
	// is close and nearly always avoids a second allocation.
	table.Grow(len(text) + len(text)/4 + 4*width + 16)
	rows := reader(text, tabs)
	for line := 0; ; line++ {
		record, err := rows.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", malformed(err)
		}
		writeRow(&table, record, width)
		if line == 0 {
			// The first row is the header, as a spreadsheet export has it.
			table.WriteByte('|')
			for range width {
				table.WriteString(" --- |")
			}
			table.WriteByte('\n')
		}
	}
	return table.String(), nil
}

func writeRow(table *strings.Builder, record []string, width int) {
	table.WriteByte('|')
	for column := range width {
		table.WriteByte(' ')
		if column < len(record) {
			writeCell(table, record[column])
		}
		table.WriteString(" |")
	}
	table.WriteByte('\n')
}

// writeCell writes one cell so that it cannot break the table: a pipe would
// start a new column and a line break would end the row.
func writeCell(table *strings.Builder, cell string) {
	cell = strings.TrimSpace(cell)
	if !strings.ContainsAny(cell, "|\r\n") {
		// Nearly every cell: no copy.
		table.WriteString(cell)
		return
	}
	for index := 0; index < len(cell); index++ {
		switch cell[index] {
		case '|':
			table.WriteString(`\|`)
		case '\r':
			// Half of a CRLF; the \n writes the break.
			if index+1 == len(cell) || cell[index+1] != '\n' {
				table.WriteString("<br>")
			}
		case '\n':
			table.WriteString("<br>")
		default:
			table.WriteByte(cell[index])
		}
	}
}

func malformed(err error) error {
	// csv's own message carries the line and column of the problem.
	return &parse.Error{Kind: parse.ParseFailed, Parser: parse.Builtin, Message: "the file is not well-formed", Err: err}
}
