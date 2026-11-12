package pagereader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
)

// article builds a page shaped like a long news or documentation page: a
// head with styles and an inline data blob, a navigation bar, an article
// of sections with links, emphasis, lists, tables and code, a sidebar and
// a footer of links. It is 157 KB and comes to 68 KB of Markdown; a typical
// article page is 100 to 600 KB.
func article() string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>How a river delta forms</title>`)
	b.WriteString("<style>" + strings.Repeat(".c{margin:0;padding:0;color:#123456}\n", 400) + "</style>")
	b.WriteString(`<script type="application/json">{"state":[` + strings.Repeat(`{"id":123456,"slug":"some-slug","tags":["a","b"]},`, 800) + `{}]}</script></head><body>`)
	b.WriteString(`<header><div class="logo"><a href="/">Site</a></div></header><nav><ul>`)
	for i := range 80 {
		fmt.Fprintf(&b, `<li class="nav-item"><a class="nav-link" href="/section/%d">Section %d</a></li>`, i, i)
	}
	b.WriteString(`</ul></nav><main><article><h1>How a river delta forms</h1>`)
	for i := range 60 {
		fmt.Fprintf(&b, `<section id="s%d"><h2>Stage %d of the delta</h2>`, i, i)
		for range 3 {
			b.WriteString(`<p class="body-text">As the river reaches the sea its <em>speed falls</em> and the sediment it carries settles, coarse sand first and <strong>fine silt</strong> further out; see <a href="/glossary#sediment">sediment</a> and <a href="https://example.org/ref/42">the survey</a> for the measurements behind this account, which were taken over eleven seasons.</p>`)
		}
		b.WriteString(`<ul><li>Channel splits into distributaries</li><li>Levees build along the banks</li><li>Bays between them fill</li></ul>`)
		if i%5 == 0 {
			b.WriteString(`<table><thead><tr><th>Year</th><th>Area (km²)</th><th>Sediment (Mt)</th></tr></thead><tbody>`)
			for y := range 8 {
				fmt.Fprintf(&b, `<tr><td>%d</td><td>%d</td><td>%d</td></tr>`, 1990+y, 1200+y*13, 40+y)
			}
			b.WriteString(`</tbody></table><pre><code>area = width * length / 2</code></pre>`)
		}
		b.WriteString(`<figure><img src="/img/delta.png" alt="delta"><figcaption>The delta at this stage.</figcaption></figure></section>`)
	}
	b.WriteString(`</article></main><aside><h3>Related</h3><ul>`)
	for i := range 40 {
		fmt.Fprintf(&b, `<li><a href="/related/%d">A related story number %d</a></li>`, i, i)
	}
	b.WriteString(`</ul></aside><footer>`)
	for i := range 120 {
		fmt.Fprintf(&b, `<a href="/footer/%d">Footer link %d</a> `, i, i)
	}
	b.WriteString(`</footer><script>window.analytics=function(){};</script></body></html>`)
	return b.String()
}

// BenchmarkExtract is HTML to Markdown for one realistic page: parsing,
// removing furniture, choosing the main part and converting it.
func BenchmarkExtract(b *testing.B) {
	page := article()
	address, _ := url.Parse("https://example.org/rivers/delta")
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for b.Loop() {
		out, err := extract(strings.NewReader(page), address)
		if err != nil || len(out.Text) < 10_000 {
			b.Fatalf("err = %v, %d bytes of text", err, len(out.Text))
		}
	}
}

// BenchmarkReadPage is one whole page over a real connection on loopback:
// resolve, guard, connect, request, read to the limit, decode, extract. No
// keep-alive, as in service, so each round opens its own connection.
func BenchmarkReadPage(b *testing.B) {
	page := article()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	}))
	defer server.Close()
	r, err := newReader(Settings{
		MaxPages: 5, MaxBytes: 2 << 20, Timeout: 20 * time.Second, MaxChars: 60_000, MaxRedirects: 5,
		UserAgent: "pagereader-bench/1.0", Allow: onlyHome,
		MaxPDFBytes: 1 << 20, PDFTimeout: time.Minute, PDFs: &pdfTexts{},
	}, func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{home}, nil })
	if err != nil {
		b.Fatal(err)
	}
	address := named(server, "site.test") + "/rivers/delta"
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for b.Loop() {
		if p := r.page(context.Background(), address, declinePDF); p.Status != Read {
			b.Fatalf("%+v", p)
		}
	}
}

// BenchmarkRenderAndCard is what happens after the fetches: five pages'
// text framed for the model and read back for the card.
func BenchmarkRenderAndCard(b *testing.B) {
	text := strings.Repeat("A sentence of a page that was read. ", 330) // about 12,000 characters
	pages := make([]Page, 5)
	for i := range pages {
		u := fmt.Sprintf("https://example.org/page/%d", i)
		pages[i] = Page{Asked: u, URL: u, Status: Read, Title: "A page", Text: text}
	}
	registry := citation.NewRegistry()
	b.ReportAllocs()
	for b.Loop() {
		out := render(pages, registry)
		if _, ok := Card(result(out)); !ok {
			b.Fatal("the card could not read the result")
		}
	}
}

// BenchmarkPublicAddress is the check every connection pays.
func BenchmarkPublicAddress(b *testing.B) {
	addresses := []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("10.0.0.1")}
	for b.Loop() {
		for _, a := range addresses {
			PublicAddress(a)
		}
	}
}
