package htmlmarkdown

import (
	"context"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func markdown(t *testing.T, html string) string {
	t.Helper()
	parser, err := New(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	document, err := parser.Parse(context.Background(), parse.StringSource("page.html", html))
	if err != nil {
		t.Fatal(err)
	}
	return document.Markdown
}

func TestStructureBecomesMarkdown(t *testing.T) {
	got := markdown(t, `<!doctype html><html><head><title>Guide</title>
<style>body{color:red}</style><script>track()</script></head>
<body><h1>Dosing guide</h1><p>Give <strong>once</strong> daily, see <a href="https://notes.example/a">the note</a>.</p>
<ul><li>First</li><li>Second</li></ul>
<pre><code>x = 1</code></pre></body></html>`)

	want := "# Dosing guide\n\n" +
		"Give **once** daily, see [the note](https://notes.example/a).\n\n" +
		"- First\n- Second\n\n" +
		"```\nx = 1\n```\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestATableStaysATableWithoutPadding(t *testing.T) {
	got := markdown(t, `<table>
<tr><th>Drug</th><th>Dose</th></tr>
<tr><td>Amoxicillin</td><td>500 mg</td></tr>
<tr><td>Ceftriaxone</td><td>1 g</td></tr>
</table>`)

	want := "| Drug | Dose |\n|---|---|\n| Amoxicillin | 500 mg |\n| Ceftriaxone | 1 g |\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestAMergedCellIsRepeatedSoEachRowStandsAlone(t *testing.T) {
	got := markdown(t, `<table>
<tr><th>Ward</th><th>Bed</th></tr>
<tr><td rowspan="2">Paediatrics</td><td>1</td></tr>
<tr><td>2</td></tr>
</table>`)

	if !strings.Contains(got, "| Paediatrics | 1 |\n| Paediatrics | 2 |") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestTheDeclaredEncodingIsHonoured(t *testing.T) {
	// "café" in Windows-1252, declared the way an old page declares it.
	got := markdown(t, "<html><head><meta charset=\"windows-1252\"></head><body><p>caf\xE9</p></body></html>")
	if got != "café\n" {
		t.Fatalf("got %q", got)
	}
}

func TestAPageWithNoTextIsRefused(t *testing.T) {
	parser, _ := New(1 << 20)
	_, err := parser.Parse(context.Background(), parse.StringSource("blank.html", "<html><body><script>x()</script></body></html>"))
	if parse.KindOf(err) != parse.NoText {
		t.Fatalf("err = %v", err)
	}
}

func TestAFileOverTheLimitIsRefused(t *testing.T) {
	parser, _ := New(64)
	page := "<p>" + strings.Repeat("word ", 100) + "</p>"

	_, err := parser.Parse(context.Background(), parse.StringSource("big.html", page))
	if parse.KindOf(err) != parse.TooLarge {
		t.Fatalf("by reported size: err = %v", err)
	}

	source := parse.StringSource("big.html", page)
	source.Size = 10 // storage was wrong; the read still stops at the limit
	_, err = parser.Parse(context.Background(), source)
	if parse.KindOf(err) != parse.TooLarge {
		t.Fatalf("by bytes read: err = %v", err)
	}
}

func TestItReadsHTMLExtensionsOnly(t *testing.T) {
	parser, _ := New(1)
	for extension, want := range map[string]bool{".html": true, ".htm": true, ".xhtml": true, ".md": false} {
		if parser.Reads(extension) != want {
			t.Errorf("Reads(%q) = %v", extension, !want)
		}
	}
}

// article is an HTML page of about a megabyte: headings, prose and a table
// per section, the mix a saved web page or an exported wiki page has.
func article() string {
	var page strings.Builder
	page.WriteString("<!doctype html><html><head><title>Article</title></head><body>")
	for page.Len() < 1<<20 {
		page.WriteString(`<h2>Section</h2><p>A paragraph with <em>some</em> emphasis and a <a href="https://notes.example/x">link</a>, long enough to be prose.</p>`)
		page.WriteString(`<table><tr><th>Name</th><th>Value</th></tr><tr><td>alpha</td><td>1</td></tr><tr><td>beta</td><td>2</td></tr></table>`)
	}
	page.WriteString("</body></html>")
	return page.String()
}

func BenchmarkParseHTML(b *testing.B) {
	parser, _ := New(64 << 20)
	page := article()
	source := parse.StringSource("article.html", page)
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse(context.Background(), source); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFragmentConvertsATableAHostedParserReturnedAsHTML(t *testing.T) {
	got, err := Fragment(`<table><thead><tr><th>Drug</th><th>Dose</th></tr></thead><tbody><tr><td>Amoxicillin</td><td>500 mg</td></tr></tbody></table>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "| Drug | Dose |\n|---|---|\n| Amoxicillin | 500 mg |" {
		t.Fatalf("got %q", got)
	}
}
