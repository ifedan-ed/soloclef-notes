package pagereader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// pdfTexts stands in for the parse service behind fileread: it records
// what it was asked to read and answers with text or a refusal.
type pdfTexts struct {
	mu      sync.Mutex
	files   []fileread.File
	choices []fileread.Choices
	text    fileread.Text
	err     error
}

func (p *pdfTexts) Read(_ context.Context, file fileread.File, choices fileread.Choices) (fileread.Text, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files = append(p.files, file)
	p.choices = append(p.choices, choices)
	return p.text, p.err
}

func (p *pdfTexts) asked() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.files)
}

// aPDF is bytes a PDF begins with; nothing here parses it.
const aPDF = "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n"

func sendAs(w http.ResponseWriter, contentType, body string) {
	if contentType == "" {
		w.Header()["Content-Type"] = nil
	} else {
		w.Header().Set("Content-Type", contentType)
	}
	_, _ = io.WriteString(w, body)
}

// The bytes decide a PDF, and a server is believed only when its bytes
// agree with it: a PDF sent as a web page or as bytes is a PDF, and a "PDF"
// that is a sign-in page or a zip is what its bytes are.
func TestAPDFIsToldByItsBytesNotByWhatTheServerCallsIt(t *testing.T) {
	junkThenPDF := strings.Repeat("x", 300) + aPDF
	htmlQuotingPDF := "<!doctype html><html><body><p>A PDF starts with %PDF-1.7, as this page says.</p></body></html>"
	for name, c := range map[string]struct {
		header, body string
		kind         string
		claimedPDF   bool
	}{
		"a PDF called one":                         {"application/pdf", aPDF, pdfType, false},
		"a PDF called a web page":                  {"text/html; charset=utf-8", aPDF, pdfType, false},
		"a PDF called plain text":                  {"text/plain", aPDF, pdfType, false},
		"a PDF called nothing":                     {"", aPDF, pdfType, false},
		"a PDF called bytes, behind junk":          {"application/octet-stream", junkThenPDF, pdfType, false},
		"a PDF called one, behind junk":            {"application/x-pdf", junkThenPDF, pdfType, false},
		"a PDF behind a byte-order mark and space": {"text/plain", "\xef\xbb\xbf \r\n" + aPDF, pdfType, false},
		"a sign-in page called a PDF":              {"application/pdf", "<html><body>Please sign in</body></html>", "text/html", true},
		"a zip called a PDF":                       {"application/pdf", "PK\x03\x04 a zip", "application/zip", true},
		"a web page that quotes the header":        {"text/html", htmlQuotingPDF, "text/html", false},
		"junk then the header, called a web page":  {"text/html", junkThenPDF, "text/html", false},
		"a header past the first kilobyte":         {"application/pdf", strings.Repeat(" x", 600) + aPDF, "text/plain", true},
	} {
		got := classify(c.header, []byte(c.body))
		if got.kind != c.kind || got.claimedPDF != c.claimedPDF {
			t.Errorf("%s: kind %q, claimed %v; want %q, %v", name, got.kind, got.claimedPDF, c.kind, c.claimedPDF)
		}
	}
}

// Through the tool: whatever a PDF was sent as, its text is read by the
// local parser for the answer, and it is cited by its address. A "PDF" that
// is a web page is read as the page it is.
func TestTheChatReadsAPDFsTextThroughTheLocalParser(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/papers/tides":
			sendAs(w, "text/html; charset=utf-8", aPDF)
		case "/download":
			w.Header().Set("Content-Disposition", `attachment; filename="Tide tables 2026.pdf"`)
			sendAs(w, "application/octet-stream", aPDF)
		case "/wall.pdf":
			sendAs(w, "application/pdf", "<html><head><title>Sign in</title></head><body><p>Please sign in to read this paper.</p></body></html>")
		}
	})
	texts := &pdfTexts{text: fileread.Text{Markdown: "# Tides\n\nThe Moon pulls on the sea.", By: parse.Local}}
	w := newWorld(t, func(s *Settings) { s.PDFs = texts })
	registry := citation.NewRegistry()
	out, err := w.call(t, registry, base+"/papers/tides", base+"/download", base+"/wall.pdf")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Read 3 of 3 pages.",
		block("[1] ", base+"/papers/tides", "success", "Title: tides.pdf\n\n# Tides\n\nThe Moon pulls on the sea."),
		block("[2] ", base+"/download", "success", "Title: Tide tables 2026.pdf\n\n# Tides\n\nThe Moon pulls on the sea."),
		block("[3] ", base+"/wall.pdf", "success", "Title: Sign in\n\nPlease sign in to read this paper."),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if texts.asked() != 2 {
		t.Fatalf("the parser was asked %d times, want the two PDFs", texts.asked())
	}
	for i, file := range texts.files {
		if string(file.Data) != aPDF || file.ContentType != pdfType || !strings.HasSuffix(file.Name, ".pdf") {
			t.Errorf("file %d = %q, %q, %d bytes", i, file.Name, file.ContentType, len(file.Data))
		}
		if texts.choices[i].Parser != string(parse.Local) || texts.choices[i].Vision != nil {
			t.Errorf("file %d was read with %+v, want the local parser and no vision model", i, texts.choices[i])
		}
	}
}

// What the parser says of a PDF it cannot read is the page's reason, and
// a PDF too large for a chat is not downloaded past its limit nor parsed.
func TestAPDFThatCannotBeReadSaysWhy(t *testing.T) {
	var served atomic.Int64
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/scan.pdf":
			sendAs(w, "application/pdf", aPDF)
		case "/book.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			n, _ := io.WriteString(w, aPDF+strings.Repeat("0", 64<<10))
			served.Add(int64(n))
		}
	})
	texts := &pdfTexts{err: &fileread.Unreadable{Reason: "The file has no text to read. It may be a scan."}}
	w := newWorld(t, func(s *Settings) { s.PDFs, s.MaxPDFBytes = texts, 4<<10 })
	out, err := w.call(t, citation.NewRegistry(), base+"/scan.pdf", base+"/book.pdf")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Read 0 of 2 pages.",
		block("", base+"/scan.pdf", "failed", "The file has no text to read. It may be a scan."),
		block("", base+"/book.pdf", "failed", "The PDF is larger than 4.0 KB, the most this tool reads. Added to a notebook as a source, it is read whole."),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if texts.asked() != 1 {
		t.Errorf("the parser was asked %d times; the PDF over the limit was parsed", texts.asked())
	}
}

// A search does not download a PDF among its results.
func TestASearchsReadOfItsResultsLeavesPDFsAlone(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/paper" {
			sendAs(w, "application/pdf", aPDF)
			return
		}
		sendHTML(w, "<html><body><p>A page.</p></body></html>")
	})
	texts := &pdfTexts{}
	w := newWorld(t, func(s *Settings) { s.PDFs = texts })
	pages := w.ReadWebPages(context.Background(), []string{base + "/paper", base + "/page"})
	if pages[0].Status != Failed || !pages[0].PDF || pages[1].Status != Read || pages[1].Text != "A page." {
		t.Fatalf("pages = %+v", pages)
	}
	if texts.asked() != 0 {
		t.Fatal("a search's read parsed a PDF")
	}
}

// Keep hands a PDF over whole, named, for the caller to store; a web page
// at the address is read as one.
func TestKeepHandsAPDFToTheKeeper(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/abs/2401.00001":
			http.Redirect(w, r, "/pdf/2401.00001v2", http.StatusFound)
		case "/pdf/2401.00001v2":
			sendAs(w, "application/octet-stream", aPDF)
		default:
			sendHTML(w, "<html><head><title>Abstract</title></head><body><p>The abstract.</p></body></html>")
		}
	})
	w := newWorld(t, nil)
	var kept bytes.Buffer
	var named, from string
	keep := func(_ context.Context, pdf PDF) error {
		named, from = pdf.Name, pdf.URL
		_, err := io.Copy(&kept, pdf.Body)
		return err
	}
	page := w.Keep(context.Background(), base+"/abs/2401.00001", 1<<20, keep)
	if page.Status != Read || !page.PDF || page.URL != base+"/pdf/2401.00001v2" || page.Text != "" {
		t.Fatalf("page = %+v", page)
	}
	if kept.String() != aPDF || named != "2401.00001v2.pdf" || from != base+"/pdf/2401.00001v2" {
		t.Fatalf("kept %d bytes named %q from %q", kept.Len(), named, from)
	}

	kept.Reset()
	page = w.Keep(context.Background(), base+"/about", 1<<20, keep)
	if page.Status != Read || page.PDF || page.Text != "The abstract." || kept.Len() != 0 {
		t.Fatalf("a web page: %+v, kept %d bytes", page, kept.Len())
	}
}

// What stops a keep is said: the file's size against the limit it was
// given, or a keeper that could not store it.
func TestAKeepThatFailsSaysWhy(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		sendAs(w, "application/pdf", aPDF+strings.Repeat("0", 8<<10))
	})
	w := newWorld(t, nil)
	var gotTooLarge error
	page := w.Keep(context.Background(), base+"/big.pdf", 2<<10, func(_ context.Context, pdf PDF) error {
		_, gotTooLarge = io.Copy(io.Discard, pdf.Body)
		return gotTooLarge
	})
	if page.Status != Failed || !page.PDF || page.Problem != "The PDF is larger than 2.0 KB, the most that is kept for one file." || !errors.Is(gotTooLarge, errPDFTooLarge) {
		t.Fatalf("too large: %+v (the keeper read %v)", page, gotTooLarge)
	}
	page = w.Keep(context.Background(), base+"/big.pdf", 1<<20, func(_ context.Context, pdf PDF) error {
		_, _ = io.Copy(io.Discard, pdf.Body)
		return errors.New("the bucket is full")
	})
	if page.Status != Failed || page.Problem != "The PDF was downloaded but could not be stored." {
		t.Fatalf("storage failed: %+v", page)
	}
}

// A PDF is given its own, longer clock once its bytes show it is one, and
// is cut off by that clock and not the page's.
func TestAPDFHasItsOwnClock(t *testing.T) {
	release := make(chan struct{})
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// The first kilobyte at once: what a PDF is is known from it.
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, aPDF+"%"+strings.Repeat("0", 2<<10)+"\n")
		w.(http.Flusher).Flush()
		switch r.URL.Path {
		case "/slow.pdf":
			// Longer than a page may take, inside a PDF's time.
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, "% the rest\n")
		case "/stalls.pdf":
			<-release
		}
	})
	t.Cleanup(func() { close(release) })
	texts := &pdfTexts{text: fileread.Text{Markdown: "Read in time."}}
	w := newWorld(t, func(s *Settings) { s.Timeout, s.PDFTimeout, s.PDFs = 100*time.Millisecond, 700*time.Millisecond, texts })
	pages := w.Read(context.Background(), []string{base + "/slow.pdf", base + "/stalls.pdf"})
	if pages[0].Status != Read || pages[0].Text != "Read in time." {
		t.Errorf("the slow PDF: %+v", pages[0])
	}
	if pages[1].Status != Failed || pages[1].Problem != "The PDF did not arrive in full within 700ms." {
		t.Errorf("the stalled PDF: %+v", pages[1])
	}
}

// insidePDF is a server on 127.0.0.2, which the tests' guard refuses,
// serving a PDF. It counts the requests that reach it; none may.
func insidePDF(t *testing.T) (base string, hits *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("this machine cannot listen on 127.0.0.2: %v", err)
	}
	hits = new(atomic.Int32)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		sendAs(w, "application/pdf", aPDF+"SECRET internal report")
	}))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server.URL, hits
}

// A PDF is no way past the guard: a private address, by number, by a name
// that resolves to one, or behind a public link's redirect, is refused
// before a connection is made, by the chat's read and by an import's keep.
func TestTheGuardRefusesAPrivatePDF(t *testing.T) {
	secret, hits := insidePDF(t)
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secret+"/report.pdf", http.StatusFound)
	})
	texts := &pdfTexts{}
	w := newWorld(t, func(s *Settings) { s.PDFs = texts })
	w.names.answers["intranet.test"] = [][]string{{"127.0.0.2"}}
	byName := strings.Replace(secret, "127.0.0.2", "intranet.test", 1) + "/report.pdf"
	addresses := []string{secret + "/report.pdf", byName, base + "/public.pdf"}

	for i, page := range w.Read(context.Background(), addresses) {
		if page.Status != Failed || !strings.HasPrefix(page.Problem, notPublic) {
			t.Errorf("read %d: %+v", i, page)
		}
	}
	kept := 0
	for _, address := range addresses {
		page := w.Keep(context.Background(), address, 1<<20, func(context.Context, PDF) error {
			kept++
			return nil
		})
		if page.Status != Failed || !strings.HasPrefix(page.Problem, notPublic) {
			t.Errorf("keep %s: %+v", address, page)
		}
	}
	if hits.Load() != 0 || kept != 0 || texts.asked() != 0 {
		t.Fatalf("%d requests reached the private server; %d kept, %d parsed", hits.Load(), kept, texts.asked())
	}
}

func TestAPDFIsNamedByTheServerOrItsAddress(t *testing.T) {
	address := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	for want, c := range map[string]struct{ url, disposition string }{
		"report.pdf":       {"https://example.org/files/report.pdf", ""},
		"Report.PDF":       {"https://example.org/files/Report.PDF", ""},
		"2401.00001v2.pdf": {"https://arxiv.org/pdf/2401.00001v2", ""},
		"example.org.pdf":  {"https://example.org/", ""},
		"Tide tables.pdf":  {"https://example.org/download?id=7", `attachment; filename="Tide tables.pdf"`},
		"Marées 2026.pdf":  {"https://example.org/d", `attachment; filename*=UTF-8''Mar%C3%A9es%202026.pdf`},
		"passwd.pdf":       {"https://example.org/d", `attachment; filename="../../etc/passwd"`},
		"evil.pdf":         {"https://example.org/d", `attachment; filename="C:\\temp\\evil.pdf"`},
		"full text.pdf":    {"https://example.org/full%20text", ""},
		"paper.pdf":        {"https://example.org/paper", `inline; filename="..."`},
	} {
		if got := pdfName(address(c.url), c.disposition); got != want {
			t.Errorf("%s %q: %q, want %q", c.url, c.disposition, got, want)
		}
	}
	long := pdfName(address("https://example.org/"+strings.Repeat("a", 400)+".pdf"), "")
	if len([]rune(long)) != maxPDFName || !strings.HasSuffix(long, ".pdf") {
		t.Errorf("a long name is %d characters: %q", len([]rune(long)), long)
	}
}
