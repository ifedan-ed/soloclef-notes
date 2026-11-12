// Package utf8text reads a text file from storage into one UTF-8 string, or
// says why it is not text.
//
// The Python tier decoded with errors="replace" and, for CSV and HTML, fell
// back to Latin-1, which accepts every byte. Both turn a file in the wrong
// encoding into quietly wrong text: every "é" becomes "Ã©" or U+FFFD, the
// document indexes, and search misses it. Here the encoding is either
// declared by the file (a byte-order mark) or it is UTF-8, and anything else
// is refused with a reason the person can act on.
package utf8text

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// Read returns the source's text. maxBytes is the largest file accepted; it
// is required because the whole text is held in memory from here on (it is
// about to be chunked), so the limit is this package's to enforce.
func Read(ctx context.Context, source parse.Source, maxBytes int64) (string, error) {
	if maxBytes < 1 {
		return "", fmt.Errorf("utf8text: maxBytes must be at least 1, got %d", maxBytes)
	}
	if source.Size > maxBytes {
		// Refused from the size storage reports, before a byte is fetched.
		return "", tooLarge(maxBytes)
	}
	file, err := source.Open(ctx)
	if err != nil {
		return "", &parse.Error{Kind: parse.Unavailable, Parser: parse.Builtin, Message: "the file could not be opened", Err: err}
	}
	defer file.Close()

	// Three bytes decide the encoding, so they are read before the rest is
	// routed through a decoder or straight into the string.
	head := make([]byte, 3)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", readFailure(err)
	}
	head = head[:n]

	var rest io.Reader
	switch {
	case bytes.HasPrefix(head, bomUTF8):
		rest = file
	case bytes.HasPrefix(head, bomUTF16LE), bytes.HasPrefix(head, bomUTF16BE):
		// ExpectBOM: the decoder reads the mark itself and takes the byte
		// order from it. Invalid surrogates come out as U+FFFD, which is
		// what the file says and not a guess of ours.
		decoder := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder()
		rest = decoder.Reader(io.MultiReader(bytes.NewReader(head), file))
	default:
		rest = io.MultiReader(bytes.NewReader(head), file)
	}

	var text strings.Builder
	if source.Size > 0 {
		// One allocation for the whole file when storage knows its size.
		text.Grow(int(source.Size))
	}
	// One byte past the limit is enough to know the file is over it, for a
	// source whose reported size was wrong.
	copied, err := io.Copy(&text, io.LimitReader(rest, maxBytes+1))
	if err != nil {
		return "", readFailure(err)
	}
	if copied > maxBytes {
		return "", tooLarge(maxBytes)
	}

	result := text.String()
	if strings.IndexByte(result, 0) >= 0 {
		return "", &parse.Error{
			Kind: parse.UnsupportedFormat, Parser: parse.Builtin,
			Message: "the file holds NUL bytes, so it is a binary file and not text",
		}
	}
	if !utf8.ValidString(result) {
		return "", &parse.Error{
			Kind: parse.UnsupportedFormat, Parser: parse.Builtin,
			Message: "the file is not UTF-8 text; save it as UTF-8 and upload it again",
		}
	}
	return result, nil
}

func tooLarge(maxBytes int64) error {
	return &parse.Error{
		Kind: parse.TooLarge, Parser: parse.Builtin,
		Message: fmt.Sprintf("the file is larger than the %d bytes a text file may be", maxBytes),
	}
}

func readFailure(err error) error {
	// Storage failing mid-read is about the moment, not the file.
	return &parse.Error{Kind: parse.Unavailable, Parser: parse.Builtin, Message: "the file could not be read", Err: err}
}
