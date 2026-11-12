// Package parse is the contract every document parser meets: a file in,
// Markdown and its page boundaries out, or a typed error saying why not.
//
// It holds no parser. The implementations are siblings (localservice,
// datalab, unstructured, llamaparse, textfile, delimited, htmlmarkdown) and
// package selection decides which one reads a given source.
package parse

import (
	"context"
	"io"
)

// ID names a parser. The four a person can choose are stored against a
// source, so their spellings are data: do not rename one.
type ID string

const (
	// Local is pymupdf4llm in soloclef_parse_service, on this server.
	Local ID = "local"
	// Datalab is Datalab's hosted Marker API.
	Datalab ID = "datalab"
	// Unstructured is Unstructured's hosted partition API.
	Unstructured ID = "unstructured"
	// LlamaParse is LlamaIndex's hosted parser.
	LlamaParse ID = "llamaparse"

	// Builtin marks a document read in Go (text, Markdown, CSV, TSV, HTML).
	// It is never a choice: those formats need no parser to choose between.
	Builtin ID = "builtin"

	// Vision marks text written by the vision model: an uploaded picture's
	// description (package vision). It is never a choice either: it is
	// what the upload's vision switch does with a picture, whichever parser
	// the source chose for its documents.
	Vision ID = "vision"
)

// Source is one file to parse, as file storage holds it.
//
// It is an opener and not a byte slice so a parser can stream the file
// straight from storage into a request body, and so a client that is allowed
// to send a request twice can read the file twice without it ever being held
// in memory.
type Source struct {
	// Name is the file's name. Only its extension is interpreted; hosted
	// parsers also receive it as the upload's filename.
	Name string
	// Size is the file's length in bytes, as storage reports it. Uploads are
	// sent with an exact Content-Length, so it must be right.
	Size int64
	// Open returns the file from its first byte. It may be called more than
	// once; each reader is closed by whoever opened it.
	Open func(ctx context.Context) (io.ReadCloser, error)
}

// Parser turns one source into a Document.
type Parser interface {
	// ID is the parser's name, recorded on every document it reads.
	ID() ID
	// Reads reports whether the parser accepts a file with this extension
	// (lower case, with its dot).
	Reads(extension string) bool
	// Parse reads the source. Every failure is an *Error.
	Parse(ctx context.Context, source Source) (Document, error)
}
