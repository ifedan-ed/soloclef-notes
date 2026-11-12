// Package multipartfile builds the multipart/form-data body the hosted
// parsers take: some form fields and one file, streamed from storage.
//
// mime/multipart can write such a body, but only into a writer, which leaves
// two ways to send it: build it in memory (the whole file in RAM, twice
// during the copy) or pipe it from a goroutine (no Content-Length, so a
// chunked upload, which not every API front end accepts). A multipart body
// is just a fixed preamble, the file's bytes and a fixed trailer, so this
// package writes the two fixed parts and lets net/http read the file between
// them: nothing is buffered and the length is exact.
package multipartfile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Field is one plain form field.
type Field struct{ Name, Value string }

// Body is a request body ready for http.NewRequest.
type Body struct {
	// Reader yields the body once. Close it if the request is never sent;
	// net/http closes it otherwise.
	Reader io.ReadCloser
	// ContentType carries the boundary.
	ContentType string
	// Length is the exact size of the body, for Content-Length.
	Length int64
}

// New opens the source and wraps it as the file part named fileField, after
// the given fields. Fields come first because a server reading the body as a
// stream then knows its options before the file arrives.
func New(ctx context.Context, fields []Field, fileField string, source parse.Source) (Body, error) {
	if source.Size < 0 {
		return Body{}, fmt.Errorf("multipartfile: %s has no size; uploads are sent with an exact Content-Length", source.Name)
	}
	var preamble bytes.Buffer
	writer := multipart.NewWriter(&preamble)
	for _, field := range fields {
		if err := writer.WriteField(field.Name, field.Value); err != nil {
			return Body{}, err
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
		quoteEscaper.Replace(fileField), quoteEscaper.Replace(source.Name)))
	header.Set("Content-Type", filekind.MIME(filekind.Extension(source.Name)))
	if _, err := writer.CreatePart(header); err != nil {
		return Body{}, err
	}
	// What multipart.Writer.Close would write after the file's bytes.
	trailer := "\r\n--" + writer.Boundary() + "--\r\n"

	file, err := source.Open(ctx)
	if err != nil {
		return Body{}, err
	}
	return Body{
		Reader: struct {
			io.Reader
			io.Closer
		}{
			io.MultiReader(&preamble, file, strings.NewReader(trailer)),
			file,
		},
		ContentType: writer.FormDataContentType(),
		Length:      int64(preamble.Len()) + source.Size + int64(len(trailer)),
	}, nil
}

// The escaping mime/multipart applies to a filename, plus line breaks, which
// would otherwise end the header.
var quoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "")
