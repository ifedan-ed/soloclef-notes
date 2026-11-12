// Package htmlmarkdown turns an HTML file into Markdown.
//
// The conversion is github.com/JohannesKaufmann/html-to-markdown, not code
// written here: turning arbitrary HTML into clean Markdown is a long tail of
// cases (nested lists, escaping, whitespace collapsing) that library has
// already met. What this package decides is what the indexing pipeline needs
// from it: real pipe tables, because the chunker keeps a table's rows with
// their header only when it can see a table, and no page furniture.
package htmlmarkdown

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html/charset"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Parser reads HTML files up to MaxBytes long.
type Parser struct {
	maxBytes  int64
	converter *converter.Converter
}

// New returns a Parser. maxBytes is required: the document tree and the
// Markdown are both held in memory.
func New(maxBytes int64) (*Parser, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("htmlmarkdown: maxBytes must be at least 1, got %d", maxBytes)
	}
	return &Parser{maxBytes: maxBytes, converter: newConverter()}, nil
}

func newConverter() *converter.Converter {
	return converter.NewConverter(converter.WithPlugins(
		// Drops what is not content: script, style, head, comments.
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		table.NewTablePlugin(
			// A merged cell's value is repeated in every cell it covered, so
			// each row still says what it is about when the chunker splits
			// the table between rows.
			table.WithSpanCellBehavior(table.SpanBehaviorMirror),
			// The library's default leaves a table with a line break in any
			// cell as raw text, which the chunker cannot see as a table.
			table.WithNewlineBehavior(table.NewlineBehaviorPreserve),
			// The default pads every cell to its column's width. That is
			// spaces a person never reads and an embedding model is billed
			// for and counts against the chunk size.
			table.WithCellPaddingBehavior(table.CellPaddingBehaviorMinimal),
			table.WithSkipEmptyRows(true),
		),
	))
}

func (*Parser) ID() parse.ID { return parse.Builtin }

func (*Parser) Reads(extension string) bool { return filekind.Of(extension) == filekind.HTML }

func (p *Parser) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	if source.Size > p.maxBytes {
		return parse.Document{}, p.tooLarge()
	}
	file, err := source.Open(ctx)
	if err != nil {
		return parse.Document{}, &parse.Error{Kind: parse.Unavailable, Parser: parse.Builtin, Message: "the file could not be opened", Err: err}
	}
	defer file.Close()

	// One byte past the limit shows a file whose reported size was wrong.
	limited := &io.LimitedReader{R: file, N: p.maxBytes + 1}
	// HTML names its own encoding (a byte-order mark or a <meta charset>),
	// and the HTML standard says how to find it and what to assume when it
	// is absent. This is that algorithm, the one a browser runs, so the
	// text indexed is the text the page shows. It is the reason HTML does
	// not go through utf8text.
	decoded, err := charset.NewReader(limited, "")
	if err != nil {
		return parse.Document{}, &parse.Error{Kind: parse.Unavailable, Parser: parse.Builtin, Message: "the file could not be read", Err: err}
	}
	// The tree is built from the stream; the file's bytes are never held as
	// one buffer beside it.
	markdown, err := p.converter.ConvertReader(decoded, converter.WithContext(ctx))
	if limited.N <= 0 {
		return parse.Document{}, p.tooLarge()
	}
	if err != nil {
		if ctx.Err() != nil {
			return parse.Document{}, ctx.Err()
		}
		return parse.Document{}, &parse.Error{Kind: parse.ParseFailed, Parser: parse.Builtin, Message: "the HTML could not be converted", Err: err}
	}
	return parse.Unpaged(parse.Builtin, strings.TrimSpace(string(markdown))+"\n")
}

func (p *Parser) tooLarge() error {
	return &parse.Error{
		Kind: parse.TooLarge, Parser: parse.Builtin,
		Message: fmt.Sprintf("the file is larger than the %d bytes an HTML file may be", p.maxBytes),
	}
}
