package pagereader

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
)

var (
	errTooManyRedirects = errors.New("pagereader: too many redirects")
	errNotWeb           = errors.New("pagereader: redirect to an address that is not http or https")
)

// thinPage is the length of text under which a page with scripts is
// flagged as probably drawn by JavaScript. It is the figure the Python
// reader used to decide a page was an empty shell.
const thinPage = 200

// maxTitle is the longest title shown, in characters. A title is a line; a
// page that puts an essay there does not get it past the text budget.
const maxTitle = 200

// The two clocks a page can be cut off by. They are causes of the page's
// context being cancelled, so why can say which limit was reached.
var (
	errPageClock = errors.New("pagereader: the page's time ran out")
	errPDFClock  = errors.New("pagereader: the PDF's time ran out")
)

// page reads one address. It never returns an error: whatever happens is a
// Page saying what happened, so one bad address cannot fail the call. A
// body that is a PDF is handed to onPDF, which is the caller's choice of
// what a PDF is for (pdf.go).
func (r *Reader) page(ctx context.Context, raw string, onPDF pdfHandler) Page {
	target, problem := address(raw)
	if target == nil {
		// Shown on a header line and on a card, so it is made one short
		// line whatever was passed.
		shown := firstRunes(strings.Map(func(c rune) rune {
			if c < ' ' || c == 0x7f {
				return ' '
			}
			return c
		}, raw), maxAddressLength)
		return Page{Asked: shown, URL: shown, Status: Failed, Problem: problem}
	}
	asked := target.String()
	failed := func(format string, args ...any) Page {
		return Page{Asked: asked, URL: asked, Status: Failed, Problem: fmt.Sprintf(format, args...)}
	}

	// One clock for the whole page: connecting, each redirect, and the
	// body. A server that sends a byte a second is cut off by it too. It is
	// a timer that cancels, not a deadline, because a body found to be a
	// PDF is given the PDF's clock instead, and a deadline cannot be moved.
	// Each connection has a deadline of its own (newReader), so the guard
	// can still share one between a name's addresses.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	pageClock := time.AfterFunc(r.settings.Timeout, func() { cancel(errPageClock) })
	defer pageClock.Stop()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asked, nil)
	if err != nil {
		return failed("This is not a readable address.")
	}
	request.Header.Set("User-Agent", r.settings.UserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,text/markdown;q=0.9,application/pdf;q=0.9,application/json;q=0.8")
	response, err := r.client.Do(request)
	if err != nil {
		return failed("%s", r.why(ctx, err))
	}
	defer response.Body.Close()

	switch status := response.StatusCode; {
	case status >= 200 && status < 300:
	case status == http.StatusNotFound || status == http.StatusGone:
		return failed("The site says there is no page at this address (HTTP %d).", status)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return failed("The site refused to show this page to an automated reader, or it needs a sign-in (HTTP %d).", status)
	case status == http.StatusTooManyRequests:
		return failed("The site is limiting how often it can be read just now (HTTP 429).")
	case status >= 500:
		return failed("The site failed to answer (HTTP %d).", status)
	default:
		return failed("The site answered HTTP %d, which is not a page.", status)
	}

	// Where the redirects led, without what a Location header may add that
	// is not part of a page's address: a place within the page, a password.
	final := *response.Request.URL
	final.Fragment, final.RawFragment, final.User = "", "", nil

	// What the body is, from the server's word and the first bytes (pdf.go
	// says why the bytes decide a PDF). Peeking reads nothing past them.
	buffered := bufio.NewReader(response.Body)
	start, _ := buffered.Peek(pdfWindow)
	found := classify(response.Header.Get("Content-Type"), start)
	if found.kind == pdfType {
		if !pageClock.Stop() {
			// The page's time ran out as the headers arrived.
			return failed("%s", r.why(ctx, context.Cause(ctx)))
		}
		pdfClock := time.AfterFunc(r.settings.PDFTimeout, func() { cancel(errPDFClock) })
		defer pdfClock.Stop()
		return onPDF(ctx, foundPDF{
			asked: asked, url: final.String(), name: pdfName(&final, response.Header.Get("Content-Disposition")),
			body: buffered, stopClock: func() { pdfClock.Stop() },
			failed: func(format string, args ...any) Page {
				page := failed(format, args...)
				page.URL, page.PDF = final.String(), true
				return page
			},
		})
	}

	// One byte more than the limit is let through, so that a body of
	// exactly the limit is told from a longer one. Nothing past that is
	// read from the socket: the body is closed with the rest unread.
	limited := &io.LimitedReader{R: buffered, N: r.settings.MaxBytes + 1}
	isHTML := false
	switch found.kind {
	case "text/html", "application/xhtml+xml":
		isHTML = true
	case "text/plain", "text/markdown", "application/json":
	default:
		if found.claimedPDF {
			return failed("The site says this address is a PDF, but what it sent is not one (it is %s).", found.kind)
		}
		return failed("This address is a file of type %s. This tool reads web pages, plain text and PDFs only.", found.kind)
	}
	declared := found.declared

	// The character set is found the way the HTML standard says a browser
	// finds it: a byte-order mark, the Content-Type header, a <meta> in
	// the first kilobyte, and failing all of those UTF-8 if the bytes are
	// valid UTF-8 and Windows-1252 if not. The text read is the text the
	// page shows.
	decoded, err := charset.NewReader(limited, declared)
	if err != nil {
		return failed("%s", r.why(ctx, err))
	}

	page := Page{Asked: asked, URL: final.String(), Status: Read}
	scripts := false
	if isHTML {
		content, err := extract(decoded, &final)
		if err != nil {
			if reason := r.interrupted(ctx, err); reason != "" {
				return failed("%s", reason)
			}
			// html.Parse fails on one thing only: a page nested deeper
			// than any real page is.
			return failed("The page's HTML could not be read.")
		}
		page.Title, page.Text, scripts = firstRunes(content.Title, maxTitle), content.Text, content.Scripts
	} else {
		text, err := io.ReadAll(decoded)
		if err != nil {
			if reason := r.interrupted(ctx, err); reason != "" {
				return failed("%s", reason)
			}
			return failed("The page could not be read to its end.")
		}
		page.Text = strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(string(text), "�"), "\r\n", "\n"))
	}

	if page.Text == "" {
		page.Status = Empty
		page.Problem = "The page was fetched and has no readable text."
		if scripts {
			page.Problem = "The page was fetched and has no text of its own: it is drawn by JavaScript, which this tool does not run."
		}
		return page
	}
	if limited.N <= 0 {
		page.Notes = append(page.Notes, fmt.Sprintf("Cut: the page is larger than %d bytes and only its first %d bytes were read.", r.settings.MaxBytes, r.settings.MaxBytes))
	}
	if scripts && utf8.RuneCountInString(page.Text) < thinPage {
		page.Notes = append(page.Notes, "The page has very little text of its own and may be drawn by JavaScript, which this tool does not run. What is below may not be all a browser shows.")
	}
	return page
}

// interrupted says why a read of the body stopped, if it stopped for a
// reason of the connection's and not of the content's.
func (r *Reader) interrupted(ctx context.Context, err error) string {
	var netErr net.Error
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &netErr) {
		return r.why(ctx, err)
	}
	return ""
}

// why turns a failed request into a sentence for the model and the person.
// The error itself is not shown: it can name addresses a name resolved to,
// and it is written for a programmer. ctx is the page's: which of its
// clocks ran out is its cause, not the request's error, which only says
// "canceled".
func (r *Reader) why(ctx context.Context, err error) string {
	var (
		refused  *refusal
		dns      *net.DNSError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		record   tls.RecordHeaderError
	)
	cause := context.Cause(ctx)
	switch {
	case errors.As(err, &refused):
		return "This address is not on the public internet, so it was not read. Addresses inside a private network, on this server or of a cloud's metadata service are never read."
	case errors.Is(err, errTooManyRedirects):
		return fmt.Sprintf("The address redirected more than %d times.", r.settings.MaxRedirects)
	case errors.Is(err, errNotWeb):
		return "The address redirected to something that is not a web address."
	case errors.Is(cause, errPageClock), errors.Is(err, errPageClock):
		return fmt.Sprintf("The site did not answer within %s.", r.settings.Timeout)
	case errors.Is(cause, errPDFClock), errors.Is(err, errPDFClock):
		return fmt.Sprintf("The PDF did not arrive in full within %s.", r.settings.PDFTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		// A connection's own deadline (newReader), or the caller's.
		return fmt.Sprintf("The site did not answer within %s.", r.settings.Timeout)
	case errors.As(err, &dns):
		return "No site was found with this name."
	case errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &record):
		return "The site's secure connection could not be verified, so it was not read."
	default:
		return "The site could not be reached."
	}
}
