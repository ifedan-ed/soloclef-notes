// Package textfile reads files that are text already: plain text, Markdown
// and source code. The text is the Markdown; nothing is converted.
package textfile

import (
	"context"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/utf8text"
)

// Parser reads text files up to MaxBytes long.
type Parser struct {
	maxBytes int64
}

// New returns a Parser. maxBytes is required: see utf8text.Read.
func New(maxBytes int64) (*Parser, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("textfile: maxBytes must be at least 1, got %d", maxBytes)
	}
	return &Parser{maxBytes: maxBytes}, nil
}

func (*Parser) ID() parse.ID { return parse.Builtin }

func (*Parser) Reads(extension string) bool { return filekind.Of(extension) == filekind.Text }

func (p *Parser) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	text, err := utf8text.Read(ctx, source, p.maxBytes)
	if err != nil {
		return parse.Document{}, err
	}
	return parse.Unpaged(parse.Builtin, text)
}
