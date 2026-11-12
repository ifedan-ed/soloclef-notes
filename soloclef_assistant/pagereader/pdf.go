package pagereader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// A PDF found at an address is read one of three ways, chosen by the
// caller and never by the reader:
//
//   - Read (the chat's tool): downloaded into memory and its text read by
//     the parse service for that one answer. Nothing is stored or indexed.
//   - Keep (an import): streamed to the caller, which stores it as the
//     document's original file; the indexing worker then parses it as it
//     parses an upload.
//   - ReadWebPages (a deep search's quotes): reported as a PDF and not
//     downloaded. A search reads five results side by side to quote a
//     sentence each; a PDF there would cost a parse per result per search.
//
// # Telling a PDF
//
// By its bytes, not by what the server calls it, because servers are wrong
// both ways: a PDF behind a CDN or a repository's download link arrives as
// application/octet-stream or even text/html, and a link that says
// application/pdf answers a sign-in or "please wait" page as often as a
// file. A PDF is what starts with "%PDF-"; a body called a PDF that is not
// one is read as whatever its bytes are.

// pdfWindow is how far into a body the PDF header is looked for. The
// standard puts "%PDF-" first, and readers (Acrobat among them) accept it
// anywhere in the first kilobyte, behind bytes some generators and servers
// put in front.
const pdfWindow = 1024

var (
	pdfMagic = []byte("%PDF-")
	// bom is UTF-8's byte-order mark, which an editor sometimes leaves in
	// front of a file it saved.
	bom = []byte("\xef\xbb\xbf")
)

// pdfType is the media type a PDF is handled as, whatever it was sent as.
const pdfType = "application/pdf"

// errPDFTooLarge is what a PDF's body returns once it has passed its limit.
var errPDFTooLarge = errors.New("pagereader: the PDF is larger than the limit")

// saysPDF reports whether a media type is the server calling the body a PDF.
func saysPDF(kind string) bool { return kind == pdfType || kind == "application/x-pdf" }

// saysBytes reports a media type that says nothing about the body but that
// it is bytes: what object stores and download links send for a file whose
// type they were never told.
func saysBytes(kind string) bool {
	return kind == "" || kind == "application/octet-stream" || kind == "binary/octet-stream"
}

// isPDF reports whether a body that starts with start is a PDF, given the
// media type the server declared (already parsed, "" for none).
//
// A body that begins with the header, after white space or a byte-order
// mark, is a PDF whatever it was called. One whose header is further in is
// taken as one only when the server also said PDF, or said only "bytes":
// an HTML page that happens to quote "%PDF-" near its top is not a PDF
// because of it.
func isPDF(kind string, start []byte) bool {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(start, bom), " \t\r\n\f\x00")
	if bytes.HasPrefix(trimmed, pdfMagic) {
		return true
	}
	if saysPDF(kind) || saysBytes(kind) {
		return bytes.Contains(start[:min(len(start), pdfWindow)], pdfMagic)
	}
	return false
}

// classified is what a body is, decided from its declared type and its
// first bytes.
type classified struct {
	// kind is the media type it is handled as: pdfType for a PDF.
	kind string
	// declared is the Content-Type the character set is read from: the
	// server's, or the one the bytes were sniffed as when the server's was
	// missing or false.
	declared string
	// claimedPDF says the server called it a PDF and it is not one.
	claimedPDF bool
}

// classify decides what a body is. The bytes decide a PDF either way; for
// anything else the server's type stands, unless it gave none or called
// the body a PDF, and then the bytes are sniffed by the rules a browser
// uses.
func classify(header string, start []byte) classified {
	kind, _, err := mime.ParseMediaType(header)
	if err != nil {
		kind = ""
	}
	if isPDF(kind, start) {
		return classified{kind: pdfType, declared: header}
	}
	if kind != "" && !saysPDF(kind) {
		return classified{kind: kind, declared: header}
	}
	sniffed := http.DetectContentType(start)
	sniffedKind, _, _ := mime.ParseMediaType(sniffed)
	return classified{kind: sniffedKind, declared: sniffed, claimedPDF: saysPDF(kind)}
}

// maxPDFName is the longest file name kept for a PDF, in characters.
const maxPDFName = 200

// pdfName is the file name a PDF is shown and stored under: the one the
// server gave it (Content-Disposition), else the last part of the address
// it was read from, else the site's name; always ending in ".pdf", because
// the parser a file goes to is chosen by its extension.
func pdfName(final *url.URL, disposition string) string {
	name := ""
	if _, parameters, err := mime.ParseMediaType(disposition); err == nil {
		name = cleanName(parameters["filename"])
	}
	if name == "" {
		name = cleanName(path.Base(final.Path))
	}
	if name == "" {
		name = cleanName(final.Hostname())
	}
	if !strings.EqualFold(path.Ext(name), ".pdf") {
		name = firstRunes(name, maxPDFName-len(".pdf")) + ".pdf"
	}
	return name
}

// cleanName is a name as a file name: no folders, no control characters,
// no leading dots or spaces, at most maxPDFName characters. "" when nothing
// of it is left.
func cleanName(raw string) string {
	raw = strings.ToValidUTF8(raw, "")
	raw = path.Base(strings.ReplaceAll(raw, "\\", "/"))
	raw = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, raw)
	raw = strings.TrimLeft(strings.TrimSpace(raw), ". ")
	if raw == "" || raw == "/" {
		return ""
	}
	if utf8.RuneCountInString(raw) > maxPDFName {
		extension := path.Ext(raw)
		if len(extension) > 16 {
			extension = ""
		}
		raw = firstRunes(strings.TrimSuffix(raw, extension), maxPDFName-utf8.RuneCountInString(extension)) + extension
	}
	return raw
}

// pdfBody is a PDF's body as a handler reads it: cut off with
// errPDFTooLarge one byte past its limit, and remembering the first error
// it gave, so that a keeper's failure can be told from the download's.
type pdfBody struct {
	body        io.Reader
	limit, read int64
	failed      error
}

func (b *pdfBody) Read(buffer []byte) (int, error) {
	if b.failed != nil {
		return 0, b.failed
	}
	// One byte past the limit, to tell a file of exactly the limit from a
	// larger one; nothing more is taken from the socket.
	if room := b.limit + 1 - b.read; int64(len(buffer)) > room {
		buffer = buffer[:room]
	}
	read, err := b.body.Read(buffer)
	b.read += int64(read)
	if b.read > b.limit {
		b.failed = errPDFTooLarge
		return 0, b.failed
	}
	if err != nil && err != io.EOF {
		b.failed = err
	}
	return read, err
}

// foundPDF is a response whose body is a PDF, handed to the way of reading
// one that the caller chose.
type foundPDF struct {
	asked string
	// url is where it was read from, after redirects.
	url  string
	name string
	// body is the PDF from its first byte.
	body io.Reader
	// stopClock stops the PDF's clock, once its bytes are all in: what
	// follows has a limit of its own.
	stopClock func()
	failed    func(format string, args ...any) Page
}

// page is the PDF as a Page that was read.
func (f foundPDF) page() Page {
	return Page{Asked: f.asked, URL: f.url, Status: Read, Title: f.name, PDF: true}
}

// pdfHandler is one way of reading a PDF.
type pdfHandler func(ctx context.Context, found foundPDF) Page

// readPDFText is how the chat's tool reads a PDF: whole into memory, then
// its text through the parse service for this answer only.
//
// The parser is Local (pymupdf4llm in soloclef_parse_service, on this
// server), always: nobody chose a parser for a link the model followed,
// and a hosted parser would bill a page count for one answer. What the
// local one costs is bounded by fileread: the size cap, 500 pages and a
// minute of parsing, and the text is then cut to this call's budget.
func (r *Reader) readPDFText(ctx context.Context, found foundPDF) Page {
	body := &pdfBody{body: found.body, limit: r.settings.MaxPDFBytes}
	data, err := io.ReadAll(body)
	switch {
	case errors.Is(err, errPDFTooLarge):
		return found.failed("The PDF is larger than %s, the most this tool reads. Added to a notebook as a source, it is read whole.", sizeText(r.settings.MaxPDFBytes))
	case err != nil:
		return found.failed("%s", r.why(ctx, err))
	}
	found.stopClock()
	text, err := r.settings.PDFs.Read(ctx, fileread.File{Name: found.name, ContentType: pdfType, Data: data},
		fileread.Choices{Parser: string(parse.Local)})
	var unreadable *fileread.Unreadable
	switch {
	case errors.As(err, &unreadable):
		return found.failed("%s", unreadable.Reason)
	case err != nil:
		// The turn ended; run reports that, not this page.
		return found.failed("The PDF could not be read.")
	}
	page := found.page()
	page.Text = strings.TrimSpace(text.Markdown)
	if page.Text == "" {
		page.Status = Empty
		page.Problem = "The PDF was fetched and has no readable text."
		return page
	}
	if text.Cut {
		page.Notes = append(page.Notes, fmt.Sprintf("Cut: the PDF's text is longer than %d characters, and only its first %d were read.", fileread.MaxChars, fileread.MaxChars))
	}
	return page
}

// declinePDF is how a search treats a PDF: reported, not downloaded.
func declinePDF(_ context.Context, found foundPDF) Page {
	return found.failed("This address is a PDF, which a search does not read. Added to a notebook as a source, it is read whole.")
}

// PDF is a PDF found at an address, as Keep hands it over.
type PDF struct {
	// URL is where it was read from, after redirects.
	URL string
	// Name is its file name, ending in ".pdf" (pdfName).
	Name string
	// Body is the PDF from its first byte. It fails once the limit Keep was
	// given is passed, and when the download breaks off.
	Body io.Reader
}

// Keeper stores a PDF Keep found. It reads Body to its end and returns
// nil once the file is stored; after any error nothing of it may remain
// stored.
type Keeper func(ctx context.Context, pdf PDF) error

// Keep reads one address as Read does, with the same guard on where it
// connects and on every redirect, except that a PDF is not read as text:
// it is handed to keep, at most maxBytes of it. The Page says how it went:
// Read with PDF set when keep stored it, Failed with the reason when it
// did not, or the web page's text when the address is a page.
func (r *Reader) Keep(ctx context.Context, address string, maxBytes int64, keep Keeper) Page {
	return r.page(ctx, address, func(ctx context.Context, found foundPDF) Page {
		body := &pdfBody{body: found.body, limit: maxBytes}
		err := keep(ctx, PDF{URL: found.url, Name: found.name, Body: body})
		switch {
		case errors.Is(body.failed, errPDFTooLarge):
			return found.failed("The PDF is larger than %s, the most that is kept for one file.", sizeText(maxBytes))
		case body.failed != nil:
			return found.failed("%s", r.why(ctx, body.failed))
		case err != nil:
			return found.failed("The PDF was downloaded but could not be stored.")
		}
		return found.page()
	})
}

// sizeText writes a byte limit the way a person reads one.
func sizeText(bytes int64) string {
	const kilobyte, megabyte = 1 << 10, 1 << 20
	switch {
	case bytes >= megabyte && bytes%megabyte == 0:
		return strconv.FormatInt(bytes/megabyte, 10) + " MB"
	case bytes >= megabyte:
		return strconv.FormatFloat(float64(bytes)/megabyte, 'f', 1, 64) + " MB"
	case bytes >= kilobyte:
		return strconv.FormatFloat(float64(bytes)/kilobyte, 'f', 1, 64) + " KB"
	}
	return strconv.FormatInt(bytes, 10) + " bytes"
}
