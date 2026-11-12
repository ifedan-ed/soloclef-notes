// Package fileread reads one file whole for a chat turn: an email's
// attachment, a file in the person's Nextcloud or Google Drive. Bytes in,
// Markdown out, or a reason in words the model can pass on.
//
// It is the indexing pipeline's parsers put to a second use, the way the
// Python tier read an email attachment (connectors/email_imap/
// attachments.py): a PDF, a Word file, a PowerPoint or an EPUB goes to the
// parser the connection chose (selection.Registry: the same routing, the
// same "this parser does not read that" as an upload), a picture goes to
// the account's vision model (package vision), and text is read as text.
// Nothing is stored and nothing is indexed: the text is the tool call's
// result and goes nowhere else.
//
// # The bounds
//
// They are the Python reader's, and properties of a chat turn rather than
// of a deployment, so they are constants:
//
//   - a file over MaxFileBytes is not read at all (the caller checks the
//     size it is told before it downloads, so the bytes never come);
//   - a zip container (Word, PowerPoint, EPUB, a spreadsheet) is checked
//     from its central directory before any parser opens it: too many
//     members, too much uncompressed, or one member compressed past
//     maxZipRatio is a decompression bomb, refused unopened;
//   - a PDF of more than maxPDFPages is refused before it is sent: laying
//     out or OCR'ing a thousand pages inside a chat turn costs the turn,
//     and at a hosted parser it costs money;
//   - one parse or description has parseTimeout;
//   - the text given back is cut at MaxChars, and says so.
//
// # Better than the Python
//
//   - The parser is the connection's choice, as for an upload: the Python
//     read every attachment with pymupdf4llm whatever was chosen.
//   - A picture is described by the account's own vision model when it
//     chose one, and else the deployment's; the Python had one per
//     workspace.
//   - A failure is a reason the model can act on ("this parser does not
//     read .xls; it reads …", "no vision model is set") instead of None
//     and a missing preview.
package fileread

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// The bounds (the package comment says why each is what it is).
const (
	// MaxFileBytes is the largest file read: the Python reader's limit on
	// an attachment, applied to every file a chat tool reads.
	MaxFileBytes = 25 << 20
	// MaxChars is the most text given back, about fifty thousand tokens.
	MaxChars = 200_000

	maxZipMembers      = 2_000
	maxZipUncompressed = 250 << 20
	maxZipRatio        = 100
	maxPDFPages        = 500
	parseTimeout       = 60 * time.Second
)

// Parsers is the routing an upload uses (*selection.Registry).
type Parsers interface {
	Default() selection.Selection
	Route(chosen selection.Selection, filename string) (parse.Parser, error)
}

// Reader reads files for the chat's tools.
type Reader struct {
	parsers Parsers
	logger  *slog.Logger
}

// New builds a Reader. Both are required: a deployment always has its
// parsers. Which vision model describes a picture is the account's, and is
// given with each file (Choices).
func New(parsers Parsers, logger *slog.Logger) (*Reader, error) {
	if parsers == nil || logger == nil {
		return nil, errors.New("fileread: the parsers and the logger are both required")
	}
	return &Reader{parsers: parsers, logger: logger}, nil
}

// File is one file, in memory.
type File struct {
	// Name is the file's name; its extension routes it.
	Name string
	// ContentType is what the file was sent or stored as, or "". It names
	// an extension for a name that has none, and says "text".
	ContentType string
	Data        []byte
}

// Choices are the account's: the parser its connection chose (as stored,
// "" for the deployment's default) and its vision model.
type Choices struct {
	Parser string
	// Vision opens the account's vision model, nil when it has none. It is
	// called only for a picture: opening a model of the account's own
	// connection reads its row and opens its key, which a PDF or a text
	// file has no use for. An *Unreadable from it is the reason given
	// (the chosen model was removed, its key cannot be opened).
	Vision func(ctx context.Context) (vision.Describer, error)
}

// Text is a file read.
type Text struct {
	Markdown string
	// Cut says the file has more text than Markdown holds.
	Cut bool
	// By is who read it: a parser, parse.Vision, or parse.Builtin for text.
	By parse.ID
}

// Unreadable is a file that was not read, with why, in words the model
// can tell the person. Every failure of Read that is not the caller's
// context ending is one of these.
type Unreadable struct {
	Reason string
}

func (u *Unreadable) Error() string { return u.Reason }

func unreadable(format string, arguments ...any) error {
	return &Unreadable{Reason: fmt.Sprintf(format, arguments...)}
}

// TooLarge is the reason given for a file over MaxFileBytes, for a caller
// that knows the size before it downloads.
func TooLarge(size int64) error {
	return unreadable("The file is %.1f MB, over the %d MB that can be read in a chat.", float64(size)/(1<<20), MaxFileBytes>>20)
}

// Read reads one file.
func (r *Reader) Read(ctx context.Context, file File, choices Choices) (Text, error) {
	if len(file.Data) > MaxFileBytes {
		return Text{}, TooLarge(int64(len(file.Data)))
	}
	if len(file.Data) == 0 {
		return Text{}, unreadable("The file is empty.")
	}
	// The bytes first: a picture is a picture whatever it is called.
	if vision.Sniff(file.Data) != vision.Unknown {
		return r.picture(ctx, file, choices.Vision)
	}
	name := file.Name
	extension := filekind.Extension(name)
	if extension == "" || (filekind.Of(extension) == filekind.Document && filekind.ExtensionOf(file.ContentType) != "" && !known(extension)) {
		// A name with no extension, or one nobody reads, sent as a type
		// that has one: routed by the type.
		if byType := filekind.ExtensionOf(file.ContentType); byType != "" {
			name, extension = name+byType, byType
		}
	}
	if textual(file.ContentType, extension) {
		return text(file.Data), nil
	}
	return r.document(ctx, file.Data, name, choices.Parser)
}

// known says an extension is one the type table has.
func known(extension string) bool { return filekind.MIME(extension) != "application/octet-stream" }

// textual is "this is text already": a text media type, or an extension
// read in Go (text, Markdown, source, CSV, TSV, HTML).
func textual(contentType, extension string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/xml" {
		return true
	}
	return extension != "" && filekind.Of(extension) != filekind.Document
}

// text is a text file as it is, valid UTF-8, cut at MaxChars.
func text(data []byte) Text {
	content := strings.ToValidUTF8(string(data), "�")
	cutText, cut := Clip(content, MaxChars)
	return Text{Markdown: cutText, Cut: cut, By: parse.Builtin}
}

// Clip is text cut to at most most characters, and whether it was cut.
func Clip(text string, most int) (string, bool) {
	if utf8.RuneCountInString(text) <= most {
		return text, false
	}
	runes := []rune(text)
	return string(runes[:most]), true
}

// picture is a picture described by the account's vision model.
func (r *Reader) picture(ctx context.Context, file File, open func(context.Context) (vision.Describer, error)) (Text, error) {
	if open == nil {
		return Text{}, unreadable("The file is a picture, and no vision model is set for this account, so it cannot be described. A vision model is chosen in Settings, Models.")
	}
	ctx, cancel := context.WithTimeout(ctx, parseTimeout)
	defer cancel()
	describer, err := open(ctx)
	if err != nil {
		var said *Unreadable
		if errors.As(err, &said) {
			return Text{}, err
		}
		return Text{}, r.failed(ctx, "opening the vision model", err)
	}
	described, err := describer.Describe(ctx, vision.Picture{Name: file.Name, Data: file.Data}, vision.Whole)
	if err != nil {
		return Text{}, r.failed(ctx, "describing a picture", err)
	}
	cutText, cut := Clip(described, MaxChars)
	return Text{Markdown: cutText, Cut: cut, By: parse.Vision}, nil
}

// document is a document read by the connection's parser.
func (r *Reader) document(ctx context.Context, data []byte, name, stored string) (Text, error) {
	chosen := r.parsers.Default()
	if stored != "" {
		var err error
		if chosen, err = selection.Of(stored); err != nil {
			return Text{}, unreadable("The connection names a parser this server does not know (%s). Choose another for the connection in Connectors.", stored)
		}
	}
	parser, err := r.parsers.Route(chosen, name)
	if err != nil {
		return Text{}, r.failed(ctx, "routing a file", err)
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		if reason := zipBomb(data); reason != "" {
			return Text{}, unreadable("The file is too large or too complex to read: %s.", reason)
		}
	}
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		if pages, counted := pdfPages(data); counted && pages > maxPDFPages {
			return Text{}, unreadable("The PDF has %d pages, more than the %d that can be read in a chat. If the user imports it, it is searched with their documents instead.", pages, maxPDFPages)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, parseTimeout)
	defer cancel()
	parsed, err := parser.Parse(ctx, parse.Source{
		Name: name, Size: int64(len(data)),
		Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil },
	})
	if err != nil {
		return Text{}, r.failed(ctx, "parsing a file", err)
	}
	cutText, cut := Clip(parsed.Markdown, MaxChars)
	return Text{Markdown: cutText, Cut: cut, By: parsed.Parser}, nil
}

// failed is a parser's or the vision model's failure in words for the
// model. The technical error goes to the log; the caller's own context
// ending is handed back as itself.
func (r *Reader) failed(ctx context.Context, doing string, err error) error {
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ctx.Err()
	}
	var failure *parse.Error
	if !errors.As(err, &failure) {
		r.logger.LogAttrs(ctx, slog.LevelError, "fileread: "+doing+" failed", slog.String("error", err.Error()))
		return unreadable("The file could not be read just now. Try again shortly.")
	}
	r.logger.LogAttrs(ctx, slog.LevelWarn, "fileread: "+doing+" failed",
		slog.String("parser", string(failure.Parser)), slog.String("kind", string(failure.Kind)), slog.String("error", err.Error()))
	what := "the parser"
	if failure.Parser == parse.Vision {
		what = "the vision model"
	}
	switch failure.Kind {
	case parse.UnsupportedFormat:
		// The registry's own words: what was refused, and what the chosen
		// parser does read.
		return unreadable("The file cannot be read: %s.", failure.Message)
	case parse.NoText:
		return unreadable("The file has no text to read. It may be a scan; a parser with OCR, chosen for the connection in Connectors, could read it.")
	case parse.TooLarge:
		return unreadable("The file is too large for %s to read.", what)
	case parse.ParseFailed:
		return unreadable("The file could not be read: %s could not make sense of it.", what)
	case parse.NotConfigured:
		if failure.Parser == parse.Vision {
			return unreadable("No vision model is set for this account, so the picture cannot be described.")
		}
		return unreadable("The parser chosen for this connection is not set up on this server. Choose another for the connection in Connectors, or ask the administrator.")
	case parse.Unauthorized:
		return unreadable("%s refused this server's key. An administrator needs to check it.", capitalised(what))
	case parse.Rejected:
		// A request that will be refused again: the endpoint said no to it,
		// or, for a model of the account's own connection, its address is
		// one a connection may not reach. Trying again would not help.
		return unreadable("%s could not be asked: %s", capitalised(what), strings.TrimSuffix(failure.Message, ".")+".")
	case parse.TimedOut:
		return unreadable("Reading the file took longer than a chat allows. If the user imports it, it is read and searched with their documents instead.")
	}
	return unreadable("The file could not be read just now: %s did not answer. Try again shortly.", what)
}

func capitalised(text string) string { return strings.ToUpper(text[:1]) + text[1:] }

// zipBomb is why a zip container must not be opened, or "". It reads the
// central directory only: no member is decompressed. A container that is
// not a valid zip is refused too, since a real Word or PowerPoint file is
// always one, and a parser would fail on it anyway.
func zipBomb(data []byte) string {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "it is not a valid archive"
	}
	if len(archive.File) > maxZipMembers {
		return fmt.Sprintf("it holds %d parts, more than %d", len(archive.File), maxZipMembers)
	}
	var total uint64
	for _, member := range archive.File {
		total += member.UncompressedSize64
		if total > maxZipUncompressed {
			return fmt.Sprintf("it unpacks to more than %d MB", maxZipUncompressed>>20)
		}
		switch {
		case member.CompressedSize64 > 0 && member.UncompressedSize64/member.CompressedSize64 > maxZipRatio:
			return "one of its parts is compressed far more than a document is"
		case member.CompressedSize64 == 0 && member.UncompressedSize64 > 0:
			// Content claimed from no bytes: no honest zip writer does it.
			return "one of its parts claims content it does not carry"
		}
	}
	return ""
}

// pdfPages is a PDF's page count, read from its page tree, and false when
// the file cannot be read that far. An unreadable tree is not a refusal:
// the parser is the judge of whether it can read the file, and its own
// time limit bounds the attempt.
func pdfPages(data []byte) (int, bool) {
	configuration := model.NewDefaultConfiguration()
	configuration.ValidationMode = model.ValidationRelaxed
	pages, err := api.PageCount(bytes.NewReader(data), configuration)
	if err != nil {
		return 0, false
	}
	return pages, true
}
