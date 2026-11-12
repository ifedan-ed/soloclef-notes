package deliverables

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// shed is a Workshop and its one Bench, in memory: files by path, and a
// renderer that answers the render command the way the sandbox's LibreOffice
// and poppler would, from what the test set. The real ones are exercised by
// TestBuiltDocumentsInARealSandbox.
type shed struct {
	files map[string][]byte
	// ran is every command, in order.
	ran []string
	// refuse makes Bench fail with it.
	refuse error
	// render is how the next rendering goes.
	render struct {
		// failed is the step the command reports failing at, or "".
		failed string
		// words is the word list pdftotext writes, whole; "" writes one
		// page element per page with no words on it. noWords writes none.
		words   string
		noWords bool
		// pages is how many pages the PDF has; 0 means one.
		pages int
		// blank lists pages with no text.
		blank []int
		fonts []string
		// otherHash makes the copy hash differently from the file.
		otherHash bool
		timedOut  bool
	}
	// runError fails every command.
	runError error
}

func (s *shed) Check() error { return nil }
func (s *shed) ID() string   { return "bench-1" }
func (s *shed) Bench(context.Context, Owner) (Bench, error) {
	if s.refuse != nil {
		return nil, s.refuse
	}
	if s.files == nil {
		s.files = map[string][]byte{}
	}
	return s, nil
}

func (s *shed) ReadFile(_ context.Context, path string, limit int64) ([]byte, error) {
	data, found := s.files[path]
	if !found {
		return nil, sandbox.ErrNotFound
	}
	if int64(len(data)) > limit {
		return nil, sandbox.ErrTooLarge
	}
	return data, nil
}

func (s *shed) WriteFile(_ context.Context, path string, data []byte) error {
	s.files[path] = append([]byte(nil), data...)
	return nil
}

// shellWord reads the single-quoted word that follows name= in a command.
func shellWord(command, name string) string {
	_, rest, _ := strings.Cut(command, name+"='")
	word, _, _ := strings.Cut(rest, "'")
	return word
}

func (s *shed) Run(_ context.Context, command string) (sandbox.Result, error) {
	s.ran = append(s.ran, command)
	if s.runError != nil {
		return sandbox.Result{}, s.runError
	}
	if strings.HasPrefix(command, "rm -rf -- ") {
		// Removes what it names: a file, or a directory and everything
		// under it.
		for _, word := range strings.Split(strings.TrimPrefix(command, "rm -rf -- "), " ") {
			target := strings.Trim(word, "'")
			for path := range s.files {
				if path == target || strings.HasPrefix(path, target+"/") {
					delete(s.files, path)
				}
			}
		}
		return sandbox.Result{}, nil
	}
	if !strings.Contains(command, markPages) {
		return sandbox.Result{Output: "ran\n"}, nil
	}
	// The render command.
	build, source := shellWord(command, "build"), shellWord(command, "src")
	if s.render.timedOut {
		return sandbox.Result{ExitCode: -1, TimedOut: true}, nil
	}
	if s.render.failed != "" {
		return sandbox.Result{Output: markFailed + s.render.failed + "\n"}, nil
	}
	data, found := s.files[source]
	if !found {
		return sandbox.Result{Output: markFailed + "copying\n"}, nil
	}
	sum := sha256.Sum256(data)
	if s.render.otherHash {
		sum = sha256.Sum256([]byte("something else"))
	}
	pages := max(s.render.pages, 1)
	var out strings.Builder
	fmt.Fprintf(&out, "%s%s\n%s%d\n", markHash, hex.EncodeToString(sum[:]), markPages, pages)
	if pages > maxVerifyPages {
		return sandbox.Result{Output: out.String()}, nil
	}
	s.files[build+"/primary.pdf"] = []byte("%PDF-1.7 rendering of " + source)
	for page := 1; page <= pages; page++ {
		chars := 400
		for _, blank := range s.render.blank {
			if blank == page {
				chars = 3
			}
		}
		fmt.Fprintf(&out, "%s%d %d\n", markChars, page, chars)
	}
	for _, font := range s.render.fonts {
		out.WriteString(markFont + font + "\n")
	}
	if !s.render.noWords {
		words := s.render.words
		if words == "" {
			words = "<doc>" + strings.Repeat(`<page width="960" height="540"></page>`, pages) + "</doc>"
		}
		s.files[build+"/"+wordsFile] = []byte(words)
	}
	return sandbox.Result{Output: out.String()}, nil
}

// zipOf is an Office file: its parts by name.
func zipOf(t testing.TB, parts map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, parts[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

const wordOpen = `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`
const wordSection = `<w:sectPr><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr>`
const wordClose = `</w:body></w:document>`

func docx(t testing.TB, body string) []byte {
	return zipOf(t, map[string]string{"[Content_Types].xml": "<Types/>", "_rels/.rels": "<Relationships/>", "word/document.xml": wordOpen + body + wordClose})
}

// goodDOCX is a document every check passes.
func goodDOCX(t testing.TB) []byte {
	return docx(t, `<w:p><w:r><w:t>The light reactions make ATP.</w:t></w:r></w:p>`+
		`<w:tbl><w:tblPr><w:tblW w:w="9000" w:type="dxa"/></w:tblPr><w:tblGrid><w:gridCol w:w="9000"/></w:tblGrid><w:tr><w:tc><w:tcPr><w:tcW w:w="9000" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>Cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`+wordSection)
}

func TestTheWordChecks(t *testing.T) {
	if got := checkDOCX(goodDOCX(t)); len(got.findings) != 0 || len(got.notes) != 0 {
		t.Fatalf("a sound document: %+v", got)
	}
	text := `<w:p><w:r><w:t>Some text.</w:t></w:r></w:p>`
	for name, test := range map[string]struct {
		data []byte
		want string
	}{
		"empty":             {nil, "DOCX is empty"},
		"not a zip":         {[]byte("hello"), "DOCX is not valid OOXML"},
		"no document part":  {zipOf(t, map[string]string{"[Content_Types].xml": "", "_rels/.rels": ""}), "DOCX is missing required parts: word/document.xml"},
		"broken XML":        {zipOf(t, map[string]string{"[Content_Types].xml": "", "_rels/.rels": "", "word/document.xml": "<w:document"}), "DOCX is not valid OOXML"},
		"no text":           {docx(t, wordSection), "DOCX body contains no text"},
		"no page setup":     {docx(t, text), "DOCX has no section page setup"},
		"no margins":        {docx(t, text+`<w:sectPr/>`), "section 1 has no page margins"},
		"a sliver margin":   {docx(t, text+`<w:sectPr><w:pgMar w:top="1440" w:right="100" w:bottom="1440" w:left="1440"/></w:sectPr>`), "section 1 has an unsafe right margin (100 twips)"},
		"a missing margin":  {docx(t, text+`<w:sectPr><w:pgMar w:top="1440" w:right="1440" w:bottom="1440"/></w:sectPr>`), "section 1 has an unsafe left margin (missing twips)"},
		"percentage table":  {docx(t, text+`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/></w:tblPr><w:tblGrid><w:gridCol w:w="1"/></w:tblGrid></w:tbl>`+wordSection), "table uses percentage width; use DXA widths"},
		"cell with no size": {docx(t, text+`<w:tbl><w:tblGrid><w:gridCol w:w="1"/></w:tblGrid><w:tr><w:tc><w:p/></w:tc></w:tr></w:tbl>`+wordSection), "table cell is missing a positive DXA width"},
		"table with no grid": {docx(t, text+`<w:tbl><w:tr><w:tc><w:tcPr><w:tcW w:w="9" w:type="dxa"/></w:tcPr></w:tc></w:tr></w:tbl>`+wordSection),
			"table is missing w:tblGrid column widths"},
		"solid shading":  {docx(t, text+`<w:p><w:pPr><w:shd w:val="solid"/></w:pPr></w:p>`+wordSection), "shading uses solid; use clear shading"},
		"typed bullet":   {docx(t, `<w:p><w:r><w:t>• First point</w:t></w:r></w:p>`+wordSection), "paragraph uses a literal bullet instead of numbering"},
		"encrypted part": {encrypted(t, goodDOCX(t)), "DOCX contains encrypted ZIP entries"},
	} {
		got := checkDOCX(test.data)
		if len(got.findings) == 0 || !contains(got.findings, test.want) {
			t.Errorf("%s: findings %q, want %q among them", name, got.findings, test.want)
		}
	}
	// A real list item that happens to start with a bullet is numbering.
	listed := docx(t, `<w:p><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>• Kept</w:t></w:r></w:p>`+wordSection)
	if got := checkDOCX(listed); len(got.findings) != 0 {
		t.Errorf("a numbered paragraph: %q", got.findings)
	}
	// A fault in every cell is one finding, and a table of contents is a
	// note that blocks nothing.
	cells := strings.Repeat(`<w:tc><w:p/></w:tc>`, 50)
	many := checkDOCX(docx(t, text+`<w:p><w:r><w:instrText> TOC \o "1-3" </w:instrText></w:r></w:p><w:tbl><w:tblGrid><w:gridCol w:w="1"/></w:tblGrid><w:tr>`+cells+`</w:tr></w:tbl>`+wordSection))
	if len(many.findings) != 1 || len(many.notes) != 1 || !strings.Contains(many.notes[0], "TOC field") {
		t.Errorf("fifty bad cells and a contents field: %+v", many)
	}
}

// encrypted marks an archive's first entry as encrypted, as a
// password-protected Office file has it.
func encrypted(t testing.TB, data []byte) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	// The general-purpose flag is two bytes at offset 6 of each local file
	// header and offset 8 of each central directory header.
	for _, header := range []struct {
		magic  string
		offset int
	}{{"PK\x03\x04", 6}, {"PK\x01\x02", 8}} {
		at := bytes.Index(out, []byte(header.magic))
		if at < 0 {
			t.Fatal("not a zip")
		}
		out[at+header.offset] |= 1
	}
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

const slideOpen = `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

func shape(x, y, w, h int) string {
	return fmt.Sprintf(`<p:sp><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm></p:spPr></p:sp>`, x, y, w, h)
}

// pptx is a deck of the given slides, each the content of a shape tree. A
// slide written as "-" is listed by the deck and has no part.
func pptx(t testing.TB, slideAttributes string, slides ...string) []byte {
	parts := map[string]string{"[Content_Types].xml": "<Types/>", "_rels/.rels": "<Relationships/>"}
	var list, links strings.Builder
	for i, tree := range slides {
		n := i + 1
		fmt.Fprintf(&list, `<p:sldId id="%d" r:id="rId%d"/>`, 255+n, n)
		fmt.Fprintf(&links, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, n, n)
		if tree != "-" {
			parts[fmt.Sprintf("ppt/slides/slide%d.xml", n)] = slideOpen + slideAttributes + `><p:cSld><p:spTree>` + tree + `</p:spTree></p:cSld></p:sld>`
		}
	}
	parts["ppt/presentation.xml"] = `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst>` +
		list.String() + `</p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/></p:presentation>`
	parts["ppt/_rels/presentation.xml.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + links.String() + `</Relationships>`
	return zipOf(t, parts)
}

func TestTheDeckChecks(t *testing.T) {
	sound := shape(100, 100, 5000, 3000)
	if got := checkPPTX(pptx(t, "", sound, sound, sound)); len(got.findings) != 0 || got.pages != 3 {
		t.Fatalf("a sound deck: %+v", got)
	}
	picture := `<p:pic><p:blipFill><a:blip r:embed="rId9"/>%s</p:blipFill><p:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="10" cy="10"/></a:xfrm></p:spPr></p:pic>`
	for name, test := range map[string]struct {
		data []byte
		want string
	}{
		"empty":                    {nil, "PPTX is empty"},
		"a slide with no part":     {pptx(t, "", sound, "-"), "slide 2 points to a missing slide part"},
		"a hidden slide":           {pptx(t, ` show="0"`, sound), "slide 1 is hidden; delete it so every delivered slide is rendered and verified"},
		"an empty slide":           {pptx(t, "", sound, ""), "slide 2 contains no drawable shapes"},
		"a shape off the canvas":   {pptx(t, "", shape(99_000_000, 0, 10, 10)), "slide 1 has a shape entirely off the canvas"},
		"a shape with no size":     {pptx(t, "", shape(0, 0, 0, 500)), "slide 1 has a shape with non-positive extent"},
		"a picture with no file":   {pptx(t, "", fmt.Sprintf(picture, "")), "slide 1 references missing embedded media"},
		"a picture cropped away":   {pptx(t, "", sound+fmt.Sprintf(picture, `<a:srcRect l="60000" r="40000"/>`)), "slide 1 has a picture crop that removes the entire image"},
		"no slides":                {pptx(t, ""), "PPTX contains no slides"},
		"geometry that is not one": {pptx(t, "", `<p:sp><p:spPr><a:xfrm><a:off x="left" y="0"/><a:ext cx="1" cy="1"/></a:xfrm></p:spPr></p:sp>`), "slide 1 has invalid shape geometry"},
	} {
		if got := checkPPTX(test.data); !contains(got.findings, test.want) {
			t.Errorf("%s: findings %q, want %q among them", name, got.findings, test.want)
		}
	}
	// A shape that bleeds off one edge is a design choice, and a line has
	// no height.
	line := `<p:cxnSp><p:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="500" cy="0"/></a:xfrm></p:spPr></p:cxnSp>`
	if got := checkPPTX(pptx(t, "", shape(-200, 0, 5000, 3000)+line)); len(got.findings) != 0 {
		t.Errorf("a bleeding shape and a line: %q", got.findings)
	}
}

func xlsx(t testing.TB, cells string) []byte {
	return xlsxAt(t, "worksheets/sheet1.xml", cells)
}

// xlsxAt is a workbook whose one sheet is named by the given target.
func xlsxAt(t testing.TB, target, cells string) []byte {
	return zipOf(t, map[string]string{
		"[Content_Types].xml": "<Types/>", "_rels/.rels": "<Relationships/>",
		"xl/workbook.xml":            `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="` + target + `"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row>` + cells + `</row></sheetData></worksheet>`,
	})
}

func TestTheWorkbookChecks(t *testing.T) {
	if got := checkXLSX(xlsx(t, `<c r="A1"><v>4</v></c><c r="B1"><f>A1*2</f><v>8</v></c><c r="C1" t="inlineStr"><is><t>Total</t></is></c>`)); len(got.findings) != 0 {
		t.Fatalf("a sound workbook: %q", got.findings)
	}
	for name, test := range map[string]struct {
		data []byte
		want string
	}{
		"empty":                      {nil, "XLSX is empty"},
		"nothing in it":              {xlsx(t, `<c r="A1"/>`), "XLSX contains no non-empty cells"},
		"a formula never calculated": {xlsx(t, `<c r="A1"><v>4</v></c><c r="B1"><f>A1*2</f></c>`), "formula cell is missing a cached result value"},
		"a formula that failed":      {xlsx(t, `<c r="A1"><f>1/0</f><v>#DIV/0!</v></c>`), "formula cell cached result is an Excel error (#DIV/0!)"},
		"no sheets": {zipOf(t, map[string]string{"[Content_Types].xml": "", "_rels/.rels": "", "xl/workbook.xml": `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`, "xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`}),
			"XLSX workbook has no worksheets"},
	} {
		if got := checkXLSX(test.data); !contains(got.findings, test.want) {
			t.Errorf("%s: findings %q, want %q among them", name, got.findings, test.want)
		}
	}
	// A sheet named from the package's root, as openpyxl names it, is the
	// same sheet.
	if got := checkXLSX(xlsxAt(t, "/xl/worksheets/sheet1.xml", `<c r="A1"><v>4</v></c>`)); len(got.findings) != 0 {
		t.Errorf("a sheet with a rooted target: %q", got.findings)
	}
	// The same fault in a thousand cells is said once.
	if got := checkXLSX(xlsx(t, strings.Repeat(`<c><f>A1</f></c>`, 1000))); len(got.findings) != 1 {
		t.Errorf("a thousand uncalculated formulas: %d findings", len(got.findings))
	}
}

func TestThePageChecks(t *testing.T) {
	sound := `<style>@import url("https://fonts.googleapis.com/css2?family=Inter"); .a{background:url(data:image/png;base64,AAAA)}</style><h1>Dose calculator</h1><input id="w"><script>let x = 1 < 2;</script>`
	if got := checkHTML([]byte(sound)); len(got.findings) != 0 || len(got.notes) != 0 {
		t.Fatalf("a sound page: %+v", got)
	}
	for name, test := range map[string]struct {
		page, want string
	}{
		"empty":            {"  \n", "HTML artifact is empty"},
		"not UTF-8":        {"<p>\xff\xfe</p>", "HTML artifact must be UTF-8 encoded"},
		"no markup":        {"just words", "HTML artifact contains no markup"},
		"a whole document": {"<!DOCTYPE html><html><body><p>x</p></body></html>", "HTML artifact must be a fragment without doctype, html, head, or body tags"},
		"a body":           {"<body><p>x</p></body>", "HTML artifact must be a fragment without doctype, html, head, or body tags"},
	} {
		if got := checkHTML([]byte(test.page)); !contains(got.findings, test.want) {
			t.Errorf("%s: findings %q, want %q", name, got.findings, test.want)
		}
	}
	// What the viewer will block is a note, once each, and blocks nothing.
	loading := `<script src="https://cdn.example.com/chart.js"></script><img src="https://example.com/a.png"><img src="https://example.com/a.png"><div style="background:url(http://example.com/b.png)">x</div><a href="#top">top</a>`
	got := checkHTML([]byte(loading))
	want := []string{
		"External resource will be blocked by the artifact viewer: https://cdn.example.com/chart.js",
		"External resource will be blocked by the artifact viewer: https://example.com/a.png",
		"External resource will be blocked by the artifact viewer: http://example.com/b.png",
	}
	if len(got.findings) != 0 || strings.Join(got.notes, "|") != strings.Join(want, "|") {
		t.Errorf("a page that loads from elsewhere: %+v", got)
	}
}

func TestAnOfficeFileIsHeldToItsBounds(t *testing.T) {
	// A part that unpacks past its limit is refused by reading it, whatever
	// size its header declares.
	big := docx(t, `<w:p><w:r><w:t>`+strings.Repeat("a", maxXMLPartBytes)+`</w:t></w:r></w:p>`+wordSection)
	if got := checkDOCX(big); len(got.findings) != 1 || !strings.Contains(got.findings[0], "exceeds") {
		t.Errorf("an oversized part: %q", got.findings)
	}
	twice := zipOf(t, map[string]string{"[Content_Types].xml": "", "_rels/.rels": "", "word/document.xml": wordOpen + wordClose})
	// The same name a second time: which of the two a reader sees depends
	// on the reader.
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/document.xml"} {
		part, _ := writer.Create(name)
		_, _ = io.WriteString(part, wordOpen+wordClose)
	}
	_ = writer.Close()
	if got := checkDOCX(buffer.Bytes()); !contains(got.findings, "DOCX contains duplicate OOXML parts") {
		t.Errorf("a duplicated part: %q (single: %q)", got.findings, checkDOCX(twice).findings)
	}
}

func TestWhatARenderingShows(t *testing.T) {
	output := "noise\n" + markHash + "abc\n" + markPages + "3\n" + markChars + "1 400\n" + markChars + "2 3\n" + markChars + "3 250\n" +
		markFont + "Helvetica\n" + markFont + "Helvetica\n"
	got := readRendering(output)
	if got.hash != "abc" || got.pages != 3 || got.chars[2] != 3 || got.failed != "" {
		t.Fatalf("read %+v", got)
	}
	report := documentFormats[FormatPDF]
	findings := renderFindings(got, report, 0)
	want := []string{
		"page 2 is blank or near-blank (3 non-whitespace characters)",
		"the document uses fonts that are not embedded, so it will look different elsewhere: Helvetica",
	}
	if strings.Join(findings, "|") != strings.Join(want, "|") {
		t.Errorf("a report's findings: %q", findings)
	}
	// A slide may be all picture, but a deck renders to exactly its slides.
	deck := documentFormats[FormatPPTX]
	got.fonts = nil
	if findings := renderFindings(got, deck, 3); len(findings) != 0 {
		t.Errorf("a deck with a picture slide: %q", findings)
	}
	if findings := renderFindings(got, deck, 4); len(findings) != 1 || findings[0] != "expected 4 page(s), found 3" {
		t.Errorf("a deck that rendered a slide short: %q", findings)
	}
	if failed := readRendering(markFailed + "converting\n"); failed.failed != "converting" {
		t.Errorf("a failed step: %+v", failed)
	}
}

func TestTheRenderCommandIsOneShellScript(t *testing.T) {
	// A path with a quote and a space in it reaches the shell as one word.
	command := renderCommand("/tmp/soloclef-verify-1", "/workspace/it's a report.docx", documentFormats[FormatDOCX])
	if !strings.Contains(command, `src='/workspace/it'\''s a report.docx'`) || !strings.Contains(command, "soffice --headless") ||
		!strings.Contains(command, `copy="$build/primary.docx"`) || !strings.Contains(command, `pdftotext -bbox-layout "$pdf" "$build/words.html"`) ||
		strings.Contains(command, "pdftoppm") {
		t.Errorf("the command:\n%s", command)
	}
	// A PDF is not converted: it is its own rendering.
	if report := renderCommand("/tmp/b", "/workspace/r.pdf", documentFormats[FormatPDF]); strings.Contains(report, "soffice") {
		t.Errorf("a PDF's command converts it:\n%s", report)
	}
}

// built is a specialist with a shed holding one file, and its tools.
func built(t *testing.T, path string, data []byte) (Specialist, *ledger, map[string]tool.Tool) {
	t.Helper()
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	l.shed.files = map[string][]byte{path: data}
	owner := Owner{ThreadID: 1, UserID: "u"}
	tools := map[string]tool.Tool{}
	for _, made := range []tool.Tool{s.instructionsTool(owner), s.executeTool(owner), s.readFileTool(owner), s.verifyTool(owner), s.saveTool(owner)} {
		tools[made.Name] = made
	}
	return s, l, tools
}

func runTool(t *testing.T, made tool.Tool, args string) (string, error) {
	t.Helper()
	return made.Run(context.Background(), toolCall(args))
}

func verdict(t *testing.T, text string) VerifyResult {
	t.Helper()
	var result VerifyResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return result
}

// The whole path of a Word document: verified, then saved with the PDF that
// was reviewed as its preview.
func TestAWordDocumentIsVerifiedThenSavedWithItsPreview(t *testing.T) {
	file := goodDOCX(t)
	_, l, tools := built(t, "/workspace/notes.docx", file)
	l.shed.render.pages = 2

	text, err := runTool(t, tools[VerifyTool], `{"path":"/workspace/notes.docx","format":"docx","description":"Checking"}`)
	if err != nil {
		t.Fatal(err)
	}
	got := verdict(t, text)
	if got.Status != "verified" || len(got.Findings) != 0 || got.PageCount == nil || *got.PageCount != 2 || len(got.Notes) != 0 {
		t.Fatalf("verdict = %s", text)
	}
	// The word list is gone and the preview is kept for the save.
	var left []string
	for path := range l.shed.files {
		left = append(left, path)
	}
	sort.Strings(left)
	if len(left) != 2 || !strings.HasSuffix(left[0], "/primary.pdf") || left[1] != "/workspace/notes.docx" {
		t.Errorf("after verification the sandbox holds %v", left)
	}
	preview := l.shed.files[left[0]]

	saved, err := runTool(t, tools[SaveTool], `{"format":"docx","title":"Lecture notes","path":"/workspace/notes.docx","markdown_representation":"# Lecture notes\r\n\r\nThe light reactions make ATP.\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"status":"saved","artifact_id":41,"generation":1,"title":"Lecture notes","format":"docx","files":[` +
		`{"file_id":241,"role":"primary","filename":"lecture-notes.docx","mime_type":"application/vnd.openxmlformats-officedocument.wordprocessingml.document","size_bytes":` + fmt.Sprint(len(file)) + `},` +
		`{"file_id":341,"role":"preview","filename":"lecture-notes.pdf","mime_type":"application/pdf","size_bytes":` + fmt.Sprint(len(preview)) + `}]}`
	if saved != want {
		t.Errorf("saved\n got %s\nwant %s", saved, want)
	}
	// The card is the same object: nothing was added for the model.
	if card, ok := ToolCard(event.Event{Kind: event.ToolResult, Tool: SaveTool, Text: saved}); !ok || rendered(t, card) != want {
		t.Errorf("card = %s", rendered(t, card))
	}
	stored := l.created[0]
	if stored.Format != "docx" || !bytes.Equal(stored.Data, file) || !bytes.Equal(stored.Preview, preview) || string(stored.Metadata) != verifiedMetadata ||
		stored.Markdown != "# Lecture notes\n\nThe light reactions make ATP." || stored.PreviewKey(41) != "artifacts/u/41/preview/0123456789abcdef0123456789abcdef.pdf" {
		t.Errorf("stored %+v", stored)
	}
	// The preview's directory is removed, and one pass stores one artifact.
	if len(l.shed.files) != 1 {
		t.Errorf("after the save the sandbox still holds %d files", len(l.shed.files))
	}
	if _, err := runTool(t, tools[SaveTool], `{"format":"docx","title":"Again","path":"/workspace/notes.docx","markdown_representation":"x"}`); err == nil || err.Error() != notVerifiedYet {
		t.Errorf("a second save of one verification: %v", err)
	}
	if len(l.created) != 1 {
		t.Fatalf("%d artifacts were stored", len(l.created))
	}
}

// Nothing is stored that verify_artifact did not pass, as the bytes it
// passed.
func TestOnlyTheVerifiedBytesAreSaved(t *testing.T) {
	const path = "/workspace/report.pdf"
	save := `{"format":"pdf","title":"Report","path":"` + path + `","markdown_representation":"# Report"}`
	verify := `{"path":"` + path + `","format":"pdf"}`
	pdf := []byte("%PDF-1.7 a report")

	t.Run("a file never verified", func(t *testing.T) {
		_, l, tools := built(t, path, pdf)
		if _, err := runTool(t, tools[SaveTool], save); err == nil || err.Error() != notVerifiedYet || len(l.created) != 0 {
			t.Errorf("err = %v, stored %d", err, len(l.created))
		}
	})
	t.Run("a file changed after it passed", func(t *testing.T) {
		_, l, tools := built(t, path, pdf)
		if text, err := runTool(t, tools[VerifyTool], verify); err != nil || verdict(t, text).Status != "verified" {
			t.Fatalf("%s, %v", text, err)
		}
		l.shed.files[path] = []byte("%PDF-1.7 a different report")
		if _, err := runTool(t, tools[SaveTool], save); err == nil || !strings.Contains(err.Error(), "changed after it was verified") || len(l.created) != 0 {
			t.Errorf("err = %v, stored %d", err, len(l.created))
		}
		// Verifying the new bytes makes them the ones that are saved.
		if text, err := runTool(t, tools[VerifyTool], verify); err != nil || verdict(t, text).Status != "verified" {
			t.Fatalf("%s, %v", text, err)
		}
		if _, err := runTool(t, tools[SaveTool], save); err != nil || len(l.created) != 1 || string(l.created[0].Data) != "%PDF-1.7 a different report" || l.created[0].Preview != nil {
			t.Errorf("after verifying again: %v, stored %+v", err, l.created)
		}
	})
	t.Run("a file that failed a later verification is no longer saved", func(t *testing.T) {
		_, l, tools := built(t, path, pdf)
		if text, err := runTool(t, tools[VerifyTool], verify); err != nil || verdict(t, text).Status != "verified" {
			t.Fatalf("%s, %v", text, err)
		}
		l.shed.render.blank = []int{1}
		if text, err := runTool(t, tools[VerifyTool], verify); err != nil || verdict(t, text).Status != "failed" {
			t.Fatalf("%s, %v", text, err)
		}
		if _, err := runTool(t, tools[SaveTool], save); err == nil || err.Error() != notVerifiedYet || len(l.created) != 0 {
			t.Errorf("err = %v, stored %d", err, len(l.created))
		}
	})
	t.Run("a preview swapped after the review", func(t *testing.T) {
		const doc = "/workspace/notes.docx"
		_, l, tools := built(t, doc, goodDOCX(t))
		if text, err := runTool(t, tools[VerifyTool], `{"path":"`+doc+`","format":"docx"}`); err != nil || verdict(t, text).Status != "verified" {
			t.Fatalf("%s, %v", text, err)
		}
		for name := range l.shed.files {
			if strings.HasSuffix(name, "/primary.pdf") {
				l.shed.files[name] = []byte("%PDF-1.7 another rendering")
			}
		}
		if _, err := runTool(t, tools[SaveTool], `{"format":"docx","title":"Notes","path":"`+doc+`","markdown_representation":"x"}`); err == nil || !strings.Contains(err.Error(), "changed after it was verified") || len(l.created) != 0 {
			t.Errorf("err = %v, stored %d", err, len(l.created))
		}
	})
	t.Run("a file verified as one format and saved as another", func(t *testing.T) {
		_, l, tools := built(t, path, pdf)
		if _, err := runTool(t, tools[VerifyTool], verify); err != nil {
			t.Fatal(err)
		}
		if _, err := runTool(t, tools[SaveTool], strings.Replace(save, `"pdf"`, `"html"`, 1)); err == nil || !strings.Contains(err.Error(), "verified as pdf, not html") || len(l.created) != 0 {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a verification an hour old", func(t *testing.T) {
		s, l, tools := built(t, path, pdf)
		if _, err := runTool(t, tools[VerifyTool], verify); err != nil {
			t.Fatal(err)
		}
		s.Verified.now = func() time.Time { return time.Now().Add(verificationLifetime + time.Second) }
		if _, err := runTool(t, tools[SaveTool], save); err == nil || err.Error() != notVerifiedYet || len(l.created) != 0 {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("arguments that do not describe a built file", func(t *testing.T) {
		_, l, tools := built(t, path, pdf)
		for args, says := range map[string]string{
			`{"format":"pdf","title":"T"}`: "path is missing",
			`{"format":"pdf","title":"T","path":"report.pdf","markdown_representation":"x"}`:              "path must be absolute",
			`{"format":"pdf","title":"T","path":"/workspace/../etc/x.pdf","markdown_representation":"x"}`: "path must be written plainly",
			`{"format":"pdf","title":"T","path":"` + path + `"}`:                                          "markdown_representation is missing",
			`{"format":"pdf","title":"","path":"` + path + `","markdown_representation":"x"}`:             "the title is empty",
			`{"format":"pdf","title":"T","path":"` + path + `","markdown_representation":"x","cards":[]}`: "remove cards, questions and markdown",
			`{"format":"quiz","title":"T","path":"` + path + `","questions":[]}`:                          "is not saved from a sandbox file; remove path",
			`{"format":"flashcards","title":"T","markdown_representation":"x","cards":[]}`:                "is not saved from a sandbox file",
		} {
			if _, err := runTool(t, tools[SaveTool], args); err == nil || !strings.Contains(err.Error(), says) {
				t.Errorf("%s: %v", args, err)
			}
		}
		if len(l.created) != 0 {
			t.Error("something was stored")
		}
	})
}

func TestWhatFailsVerification(t *testing.T) {
	const path = "/workspace/deck.pptx"
	sound := shape(100, 100, 5000, 3000)
	args := `{"path":"` + path + `","format":"pptx"}`
	for name, test := range map[string]struct {
		file  []byte
		setup func(*ledger)
		want  string
		pages int
	}{
		"a structural fault is found before anything is rendered": {file: pptx(t, "", sound, ""), want: "slide 2 contains no drawable shapes", pages: 2},
		"a deck LibreOffice cannot open":                          {file: pptx(t, "", sound), setup: func(l *ledger) { l.shed.render.failed = "converting" }, want: "LibreOffice could not convert the artifact to PDF; the file is not a valid pptx document.", pages: 1},
		"a deck that renders a slide short":                       {file: pptx(t, "", sound, sound), setup: func(l *ledger) { l.shed.render.pages = 1 }, want: "expected 2 page(s), found 1", pages: 1},
		"a file rewritten while it was rendered":                  {file: pptx(t, "", sound), setup: func(l *ledger) { l.shed.render.otherHash = true }, want: "The artifact changed while it was being verified. Verify it again when nothing is writing to it.", pages: 1},
		"a render that ran out of time":                           {file: pptx(t, "", sound), setup: func(l *ledger) { l.shed.render.timedOut = true }, want: "Rendering the artifact took too long; make it smaller or simpler.", pages: 1},
		"a word list that cannot be read":                         {file: pptx(t, "", sound), setup: func(l *ledger) { l.shed.render.noWords = true }, want: "The positions of the drawn text could not be read from the PDF.", pages: 1},
		"what the placement rules measured": {file: pptx(t, "", sound), setup: func(l *ledger) {
			l.shed.render.words = `<doc><page width="960" height="540"><flow><block><line><word xMin="900" yMin="10" xMax="990" yMax="40">Overview</word></line></block></flow></page></doc>`
		}, want: `slide 1: text runs off the slide and is cut: "Overview"`, pages: 1},
	} {
		_, l, tools := built(t, path, test.file)
		if test.setup != nil {
			test.setup(l)
		}
		text, err := runTool(t, tools[VerifyTool], args)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got := verdict(t, text)
		if got.Status != "failed" || len(got.Findings) != 1 || got.Findings[0] != test.want || got.PageCount == nil || *got.PageCount != test.pages || got.Notes == nil {
			t.Errorf("%s: %s", name, text)
		}
		// Nothing of a failed verification is left in the sandbox, and it
		// cannot be saved.
		if len(l.shed.files) != 1 {
			t.Errorf("%s: the sandbox holds %d files afterwards", name, len(l.shed.files))
		}
		if _, err := runTool(t, tools[SaveTool], `{"format":"pptx","title":"Deck","path":"`+path+`","markdown_representation":"x"}`); err == nil || len(l.created) != 0 {
			t.Errorf("%s: a failed file was saved (%v)", name, err)
		}
		// The card draws the same verdict.
		if card, ok := ToolCard(event.Event{Kind: event.ToolResult, Tool: VerifyTool, Text: text}); !ok || rendered(t, card) != text {
			t.Errorf("%s: card %s", name, rendered(t, card))
		}
	}
	_, _, tools := built(t, path, pptx(t, "", sound))

	// What is wrong with the call, not the file, is an error.
	for args, says := range map[string]string{
		`{"path":"/workspace/a.pdf","format":"video"}`:  `format "video" is not checked by verify_artifact`,
		`{"path":"/workspace/a.docx","format":"pdf"}`:   "a pdf artifact is a .pdf file",
		`{"path":"a.pdf","format":"pdf"}`:               "path must be absolute",
		`{"path":"/workspace/a\nb.pdf","format":"pdf"}`: "path must be one line",
		`{"path":"/workspace//a.pdf","format":"pdf"}`:   "path must be written plainly",
	} {
		if _, err := runTool(t, tools[VerifyTool], args); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", args, err)
		}
	}
	// A missing file is a finding: the model made it somewhere else.
	if text, err := runTool(t, tools[VerifyTool], `{"path":"/workspace/none.pdf","format":"pdf"}`); err != nil || verdict(t, text).Findings[0] != "The artifact file is missing. Generate it again before verification." {
		t.Errorf("a missing file: %s, %v", text, err)
	}
}

// A workbook and a page are checked from their bytes alone: nothing is
// rendered and no model is asked.
func TestAWorkbookAndAPageAreNotRendered(t *testing.T) {
	for format, file := range map[string][]byte{
		"xlsx": xlsx(t, `<c r="A1"><v>4</v></c>`),
		"html": []byte(`<h1>Dose</h1><img src="https://example.com/a.png">`),
	} {
		path := "/workspace/made." + format
		_, l, tools := built(t, path, file)
		text, err := runTool(t, tools[VerifyTool], `{"path":"`+path+`","format":"`+format+`"}`)
		got := verdict(t, text)
		if err != nil || got.Status != "verified" || got.PageCount != nil || len(l.shed.ran) != 0 {
			t.Errorf("%s: %s, %v, ran %v", format, text, err, l.shed.ran)
		}
		if format == "html" && (len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "will be blocked")) {
			t.Errorf("a page that loads a picture from elsewhere: notes %q", got.Notes)
		}
		saved, err := runTool(t, tools[SaveTool], `{"format":"`+format+`","title":"Made","path":"`+path+`","markdown_representation":"What it holds."}`)
		if err != nil || len(l.created) != 1 || l.created[0].Preview != nil || l.created[0].Filename != "made."+format || l.created[0].MimeType != documentFormats[format].mime {
			t.Errorf("%s: %s, %v, %+v", format, saved, err, l.created)
		}
	}
}

func TestADeploymentWithoutASandboxSaysSo(t *testing.T) {
	_, l, tools := built(t, "/workspace/a.pdf", []byte("%PDF-1.7"))
	for name, refuse := range map[string]struct {
		err  error
		says string
	}{
		"no sandbox":         {&WorkshopRefusal{Reason: noSandboxSentence}, noSandboxSentence + refusedDocumentNote},
		"the account full":   {&WorkshopRefusal{Reason: sandboxFullSentence}, sandboxFullSentence + refusedDocumentNote},
		"the server is down": {errors.New("dial tcp 10.0.0.9:8080: connection refused"), "the code sandbox could not be reached, so nothing was made; tell the requester to try again shortly"},
	} {
		l.shed.refuse = refuse.err
		for _, call := range []struct {
			tool tool.Tool
			args string
		}{
			{tools[InstructionsTool], `{"artifact_type":"pdf"}`},
			{tools[ExecuteTool], `{"code_or_command":"print(1)"}`},
			{tools[ReadFileTool], `{"path":"/workspace/a.txt"}`},
			{tools[VerifyTool], `{"path":"/workspace/a.pdf","format":"pdf"}`},
			{tools[SaveTool], `{"format":"pdf","title":"T","path":"/workspace/a.pdf","markdown_representation":"x"}`},
		} {
			if _, err := runTool(t, call.tool, call.args); err == nil || err.Error() != refuse.says {
				t.Errorf("%s, %s: %v", name, call.tool.Name, err)
			}
		}
	}
	// The other kinds are untouched by it.
	if text, err := runTool(t, tools[InstructionsTool], `{"artifact_type":"quiz"}`); err != nil || text != quizInstructions {
		t.Errorf("a quiz's instructions without a sandbox: %v", err)
	}
	// The real refusals: NoSandbox, and a specialist that has a sandbox
	// keeps the record of what passed.
	if _, err := (NoSandbox{}).Bench(context.Background(), Owner{}); err == nil || err.Error() != noSandboxSentence {
		t.Errorf("NoSandbox: %v", err)
	}
	s, _ := specialist(t, modeltest.NewScript(), &ledger{})
	withoutRecord := s
	withoutRecord.Verified = nil
	if err := withoutRecord.Check(); err == nil || !strings.Contains(err.Error(), "Verified is not set") {
		t.Errorf("a sandbox and no record: %v", err)
	}
	bare := s
	bare.Workshop, bare.Verified = NoSandbox{}, nil
	if err := bare.Check(); err != nil {
		t.Errorf("no sandbox, and so no record: %v", err)
	}
}

func TestInstructionsComeFromTheSandboxImage(t *testing.T) {
	_, l, tools := built(t, "/opt/skills/pptx/SKILL.md", []byte("# PowerPoint\nUse python-pptx."))
	text, err := runTool(t, tools[InstructionsTool], `{"artifact_type":"PPTX"}`)
	if err != nil || !strings.HasPrefix(text, "# PowerPoint\nUse python-pptx.") || !strings.Contains(text, `save_artifact(format="pptx", title="...", path=output_path, markdown_representation="...")`) ||
		!strings.Contains(text, "no load_source_document and no load_artifact_for_revision") {
		t.Errorf("instructions: %q, %v", text, err)
	}
	// An image without the skill cannot make the kind, and says so.
	if _, err := runTool(t, tools[InstructionsTool], `{"artifact_type":"docx"}`); err == nil || !strings.Contains(err.Error(), "has no instructions for docx") {
		t.Errorf("a kind the image has no skill for: %v", err)
	}
	l.shed.files["/opt/skills/xlsx/SKILL.md"] = []byte("  \n")
	if _, err := runTool(t, tools[InstructionsTool], `{"artifact_type":"xlsx"}`); err == nil || !strings.Contains(err.Error(), "has no instructions for xlsx") {
		t.Errorf("an empty skill: %v", err)
	}
}

func TestRunningCode(t *testing.T) {
	_, l, tools := built(t, "/workspace/x", nil)
	text, err := runTool(t, tools[ExecuteTool], `{"code_or_command":"print('it''s')"}`)
	if err != nil || text != "ran\n\n[Command exited with code 0]" {
		t.Fatalf("%q, %v", text, err)
	}
	// Python is written to a file and run as one process, in /workspace.
	script := "/tmp/.soloclef-exec-0123456789abcdef0123456789abcdef.py"
	if string(l.shed.files[script]) != "print('it''s')" || !strings.Contains(l.shed.ran[0], "script='"+script+"'") || !strings.Contains(l.shed.ran[0], `cd -- /workspace && `) || !strings.Contains(l.shed.ran[0], `python3 "$script"`) {
		t.Errorf("ran %q with files %v", l.shed.ran, l.shed.files)
	}
	if _, err := runTool(t, tools[ExecuteTool], `{"code_or_command":"ls -la","language":"bash"}`); err != nil || l.shed.ran[1] != "cd -- /workspace && ls -la" {
		t.Errorf("bash ran %q, %v", l.shed.ran, err)
	}
	for args, says := range map[string]string{
		`{"code_or_command":"  "}`:                         "code_or_command is empty",
		`{"code_or_command":"x","language":"ruby"}`:        `language "ruby" is not supported`,
		`{"code_or_command":"x","language":"infographic"}`: `language "infographic" is not supported`,
	} {
		if _, err := runTool(t, tools[ExecuteTool], args); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", args, err)
		}
	}
	// A sandbox that expired mid-task is said to be gone: its files are.
	l.shed.runError = fmt.Errorf("%w (404)", sandbox.ErrGone)
	if _, err := runTool(t, tools[ExecuteTool], `{"code_or_command":"ls","language":"bash"}`); err == nil || !strings.Contains(err.Error(), "the sandbox expired and its files are gone") {
		t.Errorf("an expired sandbox: %v", err)
	}
	l.shed.runError = errors.New("dial tcp 10.0.0.9: i/o timeout")
	if _, err := runTool(t, tools[ExecuteTool], `{"code_or_command":"ls","language":"bash"}`); err == nil || strings.Contains(err.Error(), "10.0.0.9") {
		t.Errorf("a failing sandbox leaked its cause: %v", err)
	}
}

func TestLongOutputIsCutAtACharacterAndKept(t *testing.T) {
	s, l, _ := built(t, "/workspace/x", nil)
	long := strings.Repeat("é", maxShownOutput+500)
	shown := s.shownOutput(context.Background(), &l.shed, sandbox.Result{Output: long, ExitCode: 2, TimedOut: true})
	full := "/tmp/soloclef-output-0123456789abcdef0123456789abcdef.txt"
	want := strings.Repeat("é", maxShownOutput) + "\n… [output truncated]\n[Command exited with code 2]\n[Full output: " + full + "]\n[The command ran past the time limit and was stopped]"
	if shown != want || string(l.shed.files[full]) != long {
		t.Errorf("shown %d bytes; kept %d", len(shown), len(l.shed.files[full]))
	}
	if short := s.shownOutput(context.Background(), &l.shed, sandbox.Result{Output: "ok\n", Truncated: true}); short != "ok\n\n… [output truncated]\n[Command exited with code 0]" {
		t.Errorf("output the sandbox client cut: %q", short)
	}
}

func TestReadingATextFile(t *testing.T) {
	_, l, tools := built(t, "/workspace/out.txt", []byte("total: 42\n"))
	if text, err := runTool(t, tools[ReadFileTool], `{"path":"/workspace/out.txt"}`); err != nil || text != "total: 42\n" {
		t.Errorf("%q, %v", text, err)
	}
	l.shed.files["/workspace/deck.pptx"] = []byte("PK\x03\x04\x00\x00")
	l.shed.files["/workspace/big.txt"] = bytes.Repeat([]byte("a"), maxReadFileBytes+1)
	for path, says := range map[string]string{
		"/workspace/none.txt":  "there is no file at /workspace/none.txt",
		"/workspace/deck.pptx": "reads UTF-8 text only",
		"/workspace/big.txt":   "is larger than",
		"relative.txt":         "path must be absolute",
	} {
		if _, err := runTool(t, tools[ReadFileTool], `{"path":"`+path+`"}`); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestTheRecordOfVerificationsIsBounded(t *testing.T) {
	record := NewVerifications()
	now := time.Now()
	record.now = func() time.Time { return now }
	for i := range maxVerifications {
		record.put("bench", fmt.Sprintf("/workspace/%d.pdf", i), verification{format: "pdf"})
		now = now.Add(time.Millisecond)
	}
	record.put("bench", "/workspace/new.pdf", verification{format: "pdf"})
	if len(record.entries) != maxVerifications {
		t.Fatalf("%d entries", len(record.entries))
	}
	if _, kept := record.get("bench", "/workspace/0.pdf"); kept {
		t.Error("the oldest entry was kept past the bound")
	}
	if _, kept := record.get("bench", "/workspace/new.pdf"); !kept {
		t.Error("the newest entry was dropped")
	}
	// The same path in another sandbox is another file.
	if _, found := record.get("other-bench", "/workspace/new.pdf"); found {
		t.Error("a verification was found under another sandbox's id")
	}
}

// The variables that point the test below at a real OpenSandbox server, the
// ones package sandbox's own real test reads. The server must be a
// throwaway.
const (
	realSandboxURL   = "SOLOCLEF_TEST_SANDBOX_URL"
	realSandboxKey   = "SOLOCLEF_TEST_SANDBOX_KEY"
	realSandboxImage = "SOLOCLEF_TEST_SANDBOX_IMAGE"
)

// TestBuiltDocumentsInARealSandbox takes each built format through the real
// tools in a real sandbox of the real image: the image's own skill is
// loaded, code is run with the image's libraries, LibreOffice and poppler
// render, the placement rules measure what they drew, and the result is
// saved. Nothing in it is a stand-in.
func TestBuiltDocumentsInARealSandbox(t *testing.T) {
	address := os.Getenv(realSandboxURL)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway OpenSandbox server", realSandboxURL)
	}
	client, err := sandbox.New(sandbox.Settings{
		// A stack of this run's own, so nothing it makes is any deployment's.
		Stack: fmt.Sprintf("test-documents-%d", time.Now().UnixNano()), URL: address, APIKey: os.Getenv(realSandboxKey), Image: os.Getenv(realSandboxImage),
		CPU: "2", Memory: "2Gi", IdleTTL: 10 * time.Minute, CommandTimeout: 3 * time.Minute, MaxPerAccount: 2,
	}, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	thread := time.Now().UnixNano()
	owner := Owner{ThreadID: thread, UserID: "u"}
	t.Cleanup(func() { _ = client.Terminate(context.Background(), thread) })

	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	s.Workshop = Sandboxes{Client: client}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	tools := map[string]tool.Tool{}
	for _, made := range []tool.Tool{s.instructionsTool(owner), s.executeTool(owner), s.readFileTool(owner), s.verifyTool(owner), s.saveTool(owner)} {
		tools[made.Name] = made
	}
	call := func(name string, args any) (string, error) {
		t.Helper()
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		text, err := tools[name].Run(context.Background(), toolCall(string(encoded)))
		t.Logf("%-26s %s", name, time.Since(started).Round(time.Millisecond))
		return text, err
	}

	// The image's own skill, which is what a model would be following.
	skill, err := call(InstructionsTool, map[string]string{"artifact_type": "pdf"})
	if err != nil || !strings.Contains(skill, "WeasyPrint") || !strings.Contains(skill, `save_artifact(format="pdf"`) {
		t.Fatalf("the pdf skill: %v\n%.300s", err, skill)
	}

	for _, format := range []struct {
		name, code string
		// pages is the page count verification reports; 0 for none.
		pages int
		// preview says a PDF preview is stored beside the file.
		preview bool
	}{
		{name: "pdf", pages: 2, code: `
from weasyprint import HTML
HTML(string="""<html><head><style>@page { size: A4; margin: 20mm } body { font-family: 'Liberation Sans'; font-size: 11pt } h1 { page-break-before: always }</style></head>
<body><h2>Photosynthesis</h2><p>The light reactions capture energy as ATP and NADPH in the thylakoid membranes.</p>
<h1>The Calvin cycle</h1><p>Carbon dioxide is fixed by RuBisCO in the stroma, using that ATP and NADPH.</p></body></html>""").write_pdf("/workspace/made.pdf")
print("written")`},
		{name: "docx", pages: 1, preview: true, code: `
from docx import Document
from docx.shared import Inches
d = Document()
for s in d.sections:
    s.top_margin = s.bottom_margin = s.left_margin = s.right_margin = Inches(1)
d.add_heading("Photosynthesis", 1)
d.add_paragraph("The light reactions capture energy as ATP and NADPH in the thylakoid membranes.")
d.save("/workspace/made.docx")`},
		{name: "pptx", pages: 2, preview: true, code: `
from pptx import Presentation
from pptx.util import Inches, Pt
p = Presentation()
for title in ("Photosynthesis", "The Calvin cycle"):
    slide = p.slides.add_slide(p.slide_layouts[6])
    box = slide.shapes.add_textbox(Inches(1), Inches(1), Inches(8), Inches(1.5))
    box.text_frame.text = title
    box.text_frame.paragraphs[0].runs[0].font.size = Pt(40)
p.save("/workspace/made.pptx")`},
		{name: "xlsx", code: `
from openpyxl import Workbook
w = Workbook()
s = w.active
s.append(["Stage", "ATP"]); s.append(["Light reactions", 18]); s.append(["Calvin cycle", -18])
w.save("/workspace/made.xlsx")`},
		{name: "html", code: `
open("/workspace/made.html", "w").write("<style>.c{font:16px sans-serif}</style><div class='c'><h1>Dose</h1><label>Weight <input id='w' type='number'></label><output id='o'></output></div><script>document.getElementById('w').oninput=e=>document.getElementById('o').textContent=e.target.value*15</script>")`},
	} {
		t.Run(format.name, func(t *testing.T) {
			path := "/workspace/made." + format.name
			if out, err := call(ExecuteTool, map[string]string{"code_or_command": format.code}); err != nil || !strings.HasSuffix(out, "[Command exited with code 0]") {
				t.Fatalf("building: %v\n%s", err, out)
			}
			text, err := call(VerifyTool, map[string]string{"path": path, "format": format.name})
			if err != nil {
				t.Fatalf("verifying: %v", err)
			}
			got := verdict(t, text)
			if got.Status != "verified" || (format.pages == 0) != (got.PageCount == nil) || (got.PageCount != nil && *got.PageCount != format.pages) {
				t.Fatalf("verdict: %s", text)
			}
			saved, err := call(SaveTool, map[string]string{"format": format.name, "title": "Made " + format.name, "path": path, "markdown_representation": "# Photosynthesis\n\nThe light reactions capture energy."})
			if err != nil {
				t.Fatalf("saving: %v", err)
			}
			stored := l.created[len(l.created)-1]
			if stored.Format != format.name || len(stored.Data) == 0 || (len(stored.Preview) > 0) != format.preview {
				t.Errorf("stored format %s, %d bytes, preview %d bytes; saved %s", stored.Format, len(stored.Data), len(stored.Preview), saved)
			}
			if format.preview && !bytes.HasPrefix(stored.Preview, []byte("%PDF-")) {
				t.Error("the preview is not a PDF")
			}
		})
	}

	t.Run("what the real renderers catch", func(t *testing.T) {
		// A PDF with an empty page, and one in a font that is not embedded
		// (ReportLab's built-in Helvetica).
		code := `
from reportlab.pdfgen import canvas
c = canvas.Canvas("/workspace/bad.pdf")
c.setFont("Helvetica", 12)
c.drawString(72, 720, "The light reactions capture energy as ATP and NADPH.")
c.showPage()
c.showPage()
c.save()`
		if out, err := call(ExecuteTool, map[string]string{"code_or_command": code}); err != nil || !strings.Contains(out, "code 0]") {
			t.Fatalf("%v\n%s", err, out)
		}
		text, err := call(VerifyTool, map[string]string{"path": "/workspace/bad.pdf", "format": "pdf"})
		if err != nil {
			t.Fatal(err)
		}
		got := verdict(t, text)
		if got.Status != "failed" || !contains(got.Findings, "page 2 is blank or near-blank (0 non-whitespace characters)") ||
			!strings.Contains(strings.Join(got.Findings, "|"), "fonts that are not embedded") {
			t.Errorf("verdict: %s", text)
		}
		// A file that is not what its name says does not survive LibreOffice.
		if _, err := call(ExecuteTool, map[string]string{"language": "bash", "code_or_command": "cp /workspace/made.xlsx /workspace/fake.docx"}); err != nil {
			t.Fatal(err)
		}
		text, err = call(VerifyTool, map[string]string{"path": "/workspace/fake.docx", "format": "docx"})
		if err != nil || verdict(t, text).Status != "failed" {
			t.Errorf("a workbook named .docx: %s, %v", text, err)
		}
		// A failing command is output and an exit code, not an error.
		if out, err := call(ExecuteTool, map[string]string{"code_or_command": "raise SystemExit(3)"}); err != nil || !strings.HasSuffix(out, "[Command exited with code 3]") {
			t.Errorf("a failing script: %q, %v", out, err)
		}
	})

	t.Run("a deck's placement, measured", func(t *testing.T) {
		// One deck built the way a model builds one, twice: laid out
		// cleanly, and with the faults a look at pictures was relied on to
		// catch. Its previews are kept in SOLOCLEF_TEST_KEEP_PREVIEWS, when
		// set, to be looked at.
		if out, err := call(ExecuteTool, map[string]string{"code_or_command": placementDeck}); err != nil || !strings.HasSuffix(out, "[Command exited with code 0]") {
			t.Fatalf("building: %v\n%s", err, out)
		}
		text, err := call(VerifyTool, map[string]string{"path": "/workspace/clean.pptx", "format": "pptx"})
		if err != nil || verdict(t, text).Status != "verified" {
			t.Fatalf("the clean deck: %s, %v", text, err)
		}
		if _, err := call(SaveTool, map[string]string{"format": "pptx", "title": "Clean deck", "path": "/workspace/clean.pptx", "markdown_representation": "# The cell cycle"}); err != nil {
			t.Fatal(err)
		}
		keep(t, "clean.pdf", l.created[len(l.created)-1].Preview)

		text, err = call(VerifyTool, map[string]string{"path": "/workspace/faulty.pptx", "format": "pptx"})
		if err != nil {
			t.Fatal(err)
		}
		got := verdict(t, text)
		all := strings.Join(got.Findings, "\n")
		t.Logf("the faulty deck's findings:\n%s", all)
		for _, want := range []string{
			`slide 1: the line "Staircase" crosses the text of "Source"`,
			`slide 1: the line "Staircase" crosses the text of "Moon label"`,
			`slide 2: the text of "Crowded" ("G1: the cell grows S: DNA is copied G2: checks before div...") is not drawn whole inside its box`,
			`slide 2: words are drawn over each other`,
			`slide 2: "Fine print" in Fine print is set at 9 pt`,
			`slide 2: "Faint words on a pale ground" in Faint is #C8C8C8 on #FFFFFF`,
			`slide 2: the picture "Chart" runs off the slide`,
			`slide 2: the text of "Covered" ("Hidden behind the card") is hidden under the shape "Late card"`,
		} {
			if !strings.Contains(all, want) {
				t.Errorf("no finding %q", want)
			}
		}
		// What was laid out well is not blamed.
		for _, sound := range []string{"Title", "Subtitle", "Heading", "Interphase", "Mitosis", "Link", "Hours"} {
			if strings.Contains(all, `"`+sound+`"`) {
				t.Errorf("a finding names %q", sound)
			}
		}
		if got.Status != "failed" {
			t.Errorf("status %s", got.Status)
		}
		// The faulty deck's own drawing, kept for the eye: verification
		// leaves nothing behind, so it is converted here as the render
		// command converts it.
		if out, err := call(ExecuteTool, map[string]string{"language": "bash", "code_or_command": "cd /workspace && soffice --headless -env:UserInstallation=file:///tmp/keep-profile --convert-to pdf faulty.pptx >/dev/null 2>&1; ls -l faulty.pdf"}); err == nil && strings.Contains(out, "faulty.pdf") {
			box := Sandboxes{Client: client}
			if pdf, err := box.read(t, owner, "/workspace/faulty.pdf"); err == nil {
				keep(t, "faulty.pdf", pdf)
			}
		}
	})
}

// keep writes a preview where SOLOCLEF_TEST_KEEP_PREVIEWS says, to be
// rendered and looked at.
func keep(t *testing.T, name string, data []byte) {
	t.Helper()
	if dir := os.Getenv("SOLOCLEF_TEST_KEEP_PREVIEWS"); dir != "" {
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			t.Error(err)
		}
	}
}

// read reads a sandbox file for a test.
func (s Sandboxes) read(t *testing.T, owner Owner, path string) ([]byte, error) {
	t.Helper()
	bench, err := s.Bench(context.Background(), owner)
	if err != nil {
		return nil, err
	}
	return bench.ReadFile(context.Background(), path, MaxDocumentBytes)
}

// placementDeck builds two decks of two slides with python-pptx: clean.pptx,
// laid out as the pptx skill asks (cards, connectors that end at their
// cards, a picture, a table, a footer), and faulty.pptx, the same with the
// faults the placement rules exist for.
const placementDeck = `
from pptx import Presentation
from pptx.util import Inches, Pt, Emu
from pptx.dml.color import RGBColor
from pptx.enum.shapes import MSO_SHAPE, MSO_CONNECTOR
from pptx.enum.text import MSO_AUTO_SIZE
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

fig, ax = plt.subplots(figsize=(4, 3), dpi=100)
ax.bar(["G1", "S", "G2", "M"], [11, 8, 4, 1], color="#2F6690")
ax.set_ylabel("hours")
fig.tight_layout()
fig.savefig("/workspace/chart.png")

INK, PAPER, ACCENT, MUTED = RGBColor(0x1B, 0x26, 0x3B), RGBColor(0xFF, 0xFF, 0xFF), RGBColor(0x2F, 0x66, 0x90), RGBColor(0x55, 0x5F, 0x6B)

def text(slide, name, x, y, w, h, words, size, colour=INK, bold=False, auto=False):
    box = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
    box.name = name
    frame = box.text_frame
    frame.word_wrap = True
    if not auto:
        # A box that keeps its size, as the pptx skill asks: text that does
        # not fit runs out of it.
        frame.auto_size = MSO_AUTO_SIZE.NONE
    for side in ("margin_left", "margin_right", "margin_top", "margin_bottom"):
        setattr(frame, side, 0)
    paragraphs = words if isinstance(words, list) else [words]
    for i, line in enumerate(paragraphs):
        p = frame.paragraphs[0] if i == 0 else frame.add_paragraph()
        run = p.add_run()
        run.text = line
        run.font.size, run.font.bold, run.font.name = Pt(size), bold, "Arial"
        run.font.color.rgb = colour
    return box

def card(slide, name, x, y, w, h, words, fill=ACCENT):
    shape = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(x), Inches(y), Inches(w), Inches(h))
    shape.name = name
    shape.fill.solid()
    shape.fill.fore_color.rgb = fill
    shape.line.fill.background()
    frame = shape.text_frame
    frame.word_wrap = True
    run = frame.paragraphs[0].add_run()
    run.text = words
    run.font.size, run.font.name, run.font.bold = Pt(20), "Arial", True
    run.font.color.rgb = PAPER
    return shape

def build(path, faulty):
    deck = Presentation()
    deck.slide_width, deck.slide_height = Inches(13.333), Inches(7.5)
    blank = deck.slide_layouts[6]

    # Slide 1: a title, a staircase to "the moon", and a source line.
    one = deck.slides.add_slide(blank)
    text(one, "Title", 0.8, 1.2, 8, 1.2, "The cell cycle", 54, bold=True)
    text(one, "Subtitle", 0.8, 2.6, 7, 1.2, "Four phases, one division: how a cell copies itself and splits in two.", 24, colour=MUTED)
    moon = one.shapes.add_shape(MSO_SHAPE.OVAL, Inches(11.4), Inches(2.4), Inches(0.6), Inches(0.6))
    moon.name = "Moon"
    moon.fill.solid(); moon.fill.fore_color.rgb = ACCENT
    text(one, "Moon label", 10.9, 3.1, 1.6, 0.4, "the moon", 14, colour=MUTED)
    # The staircase climbs to the foot of "the moon"'s label; a faulty one
    # starts down in the source line and runs on up through the label.
    if faulty:
        points = [(3.0, 7.1), (4.2, 5.4), (10.0, 5.4), (10.0, 4.4), (11.3, 4.4), (11.3, 2.4)]
    else:
        points = [(8.0, 6.2), (8.8, 5.4), (10.0, 5.4), (10.0, 4.4), (11.3, 4.4), (11.3, 3.6)]
    steps = one.shapes.build_freeform(Inches(points[0][0]), Inches(points[0][1]))
    steps.add_line_segments([(Inches(x), Inches(y)) for x, y in points[1:]], close=False)
    stairs = steps.convert_to_shape()
    stairs.name = "Staircase"
    stairs.fill.background()
    stairs.line.color.rgb = ACCENT
    stairs.line.width = Pt(1.5)
    text(one, "Source", 0.8, 6.7, 11.5, 0.5, "Source: Alberts, Molecular Biology of the Cell, 7th edition, chapter 17.", 12, colour=MUTED, auto=True)

    # Slide 2: two cards joined by a connector, a chart, a table, a footer.
    two = deck.slides.add_slide(blank)
    text(two, "Heading", 0.8, 0.5, 11.7, 0.9, "What happens in each phase", 36, bold=True)
    card(two, "Interphase", 0.8, 1.8, 3.2, 1.2, "Interphase: grow and copy")
    card(two, "Mitosis", 5.0, 1.8, 3.2, 1.2, "Mitosis: divide the nucleus")
    link = two.shapes.add_connector(MSO_CONNECTOR.STRAIGHT, Inches(4.0), Inches(2.4), Inches(5.0), Inches(2.4))
    link.name = "Link"
    link.line.color.rgb = INK
    bullets = ["G1: the cell grows", "S: DNA is copied", "G2: checks before division"]
    if faulty:
        bullets = bullets * 4
    text(two, "Crowded" if faulty else "Phases", 0.8, 3.4, 7.4, 1.6 if faulty else 1.6, bullets, 20)
    if faulty:
        text(two, "Fine print", 0.8, 5.3, 6, 0.4, "Fine print", 9)
        text(two, "Faint", 0.8, 5.8, 6, 0.5, "Faint words on a pale ground", 20, colour=RGBColor(0xC8, 0xC8, 0xC8))
        text(two, "Covered", 9.0, 6.4, 3.5, 0.5, "Hidden behind the card", 18)
        late = two.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(8.9), Inches(6.3), Inches(3.8), Inches(0.8))
        late.name = "Late card"
        late.fill.solid(); late.fill.fore_color.rgb = RGBColor(0xEE, 0xF2, 0xF7)
        late.line.fill.background()
    picture = two.shapes.add_picture("/workspace/chart.png", Inches(9.0 if not faulty else 10.5), Inches(1.6), width=Inches(3.6))
    picture.name = "Chart"
    rows = two.shapes.add_table(3, 2, Inches(9.0), Inches(4.6), Inches(3.6), Inches(1.2))
    rows.name = "Hours"
    for r, (a, b) in enumerate([("Phase", "Hours"), ("G1", "11"), ("S", "8")]):
        for c, value in enumerate((a, b)):
            cell = rows.table.cell(r, c)
            cell.text = value
            cell.text_frame.paragraphs[0].runs[0].font.size = Pt(14)
    deck.save(path)

build("/workspace/clean.pptx", False)
build("/workspace/faulty.pptx", True)
print("built")
`

// The specialist as the main agent runs it, for a report: the four steps in
// order through the nested loop, a first verification that fails and is
// repaired, and the cards the page draws (the verdict twice, then the file).
func TestTheSpecialistMakesAReportEndToEnd(t *testing.T) {
	l := &ledger{}
	const path = "/workspace/report.pdf"
	verify := `{"path":"` + path + `","format":"pdf"}`
	nestedModel := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"pdf"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "e1", Name: ExecuteTool, Args: `{"code_or_command":"build()"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "v1", Name: VerifyTool, Args: verify}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "e2", Name: ExecuteTool, Args: `{"code_or_command":"build(fixed=True)"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "v2", Name: VerifyTool, Args: verify}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s1", Name: SaveTool, Args: `{"format":"pdf","title":"Photosynthesis","path":"` + path + `","markdown_representation":"# Photosynthesis\n\nLight reactions."}`}}},
		modeltest.Reply{Text: "Saved a two-page PDF report titled Photosynthesis."},
	)
	s, _ := specialist(t, nestedModel, l)
	// A built document takes more steps than a deck: seven model calls and
	// the six tool calls between them.
	s.Limits.MaxSteps = 13
	l.shed.files = map[string][]byte{"/opt/skills/pdf/SKILL.md": []byte("# PDF\nUse WeasyPrint."), path: []byte("%PDF-1.7 a report")}
	l.shed.render.pages = 2
	// The first time the pages are measured, a word on page 2 runs past
	// its right edge.
	l.shed.render.words = `<doc><page width="595" height="842"></page><page width="595" height="842"><flow><block><line>` +
		`<word xMin="560" yMin="100" xMax="640" yMax="112">Total</word></line></block></flow></page></doc>`
	delegate, err := s.Delegate(Owner{ThreadID: 1, UserID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	mainModel := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "d1", Name: DelegateName, Args: `{"task":"Make a PDF report on photosynthesis from: ..."}`}}},
		modeltest.Reply{Text: "Your report is ready."},
	)
	main, err := agent.New(agent.Config{Model: mainModel, MaxSteps: 6, MaxParallelTools: 1, Store: s.Store, Delegates: []agent.Delegate{delegate}})
	if err != nil {
		t.Fatal(err)
	}
	var cards []string
	result, err := main.Start(context.Background(), "chat-1", []model.Message{model.User("a report please")}, func(ev event.Event) {
		if ev.Kind != event.ToolResult {
			return
		}
		if ev.Tool == VerifyTool && ev.CallID == "d1/v1" {
			// The repair: the next verification finds nothing.
			l.shed.render.words = ""
		}
		if card, drawn := ToolCard(ev); drawn {
			cards = append(cards, ev.Tool+" "+rendered(t, card))
		}
	})
	if err != nil || result.Answer != "Your report is ready." {
		t.Fatalf("result %+v, %v", result, err)
	}
	want := []string{
		`verify_artifact {"status":"failed","findings":["page 2: text runs off the page and is cut: \"Total\""],"notes":[],"page_count":2}`,
		`verify_artifact {"status":"verified","findings":[],"notes":[],"page_count":2}`,
		`save_artifact {"status":"saved","artifact_id":41,"generation":1,"title":"Photosynthesis","format":"pdf","files":[{"file_id":241,"role":"primary","filename":"photosynthesis.pdf","mime_type":"application/pdf","size_bytes":17}]}`,
	}
	if strings.Join(cards, "\n") != strings.Join(want, "\n") {
		t.Errorf("cards:\n%s\nwant:\n%s", strings.Join(cards, "\n"), strings.Join(want, "\n"))
	}
	// The model was handed the image's skill, and each failure's findings.
	requests := nestedModel.Requests()
	if told := requests[1].Messages[len(requests[1].Messages)-1].Content; !strings.HasPrefix(told, "# PDF\nUse WeasyPrint.") {
		t.Errorf("the instructions the model read: %q", told)
	}
	if told := requests[3].Messages[len(requests[3].Messages)-1].Content; !strings.Contains(told, "runs off the page") {
		t.Errorf("after the failed verification the model was told %q", told)
	}
	if len(l.created) != 1 || l.created[0].Format != "pdf" || l.created[0].Owner.ThreadID != 1 {
		t.Errorf("stored %+v", l.created)
	}
}

// The checks' cost to this process: a document of the size a real one has.
func BenchmarkCheckDOCX(b *testing.B) {
	paragraph := `<w:p><w:r><w:t>The light reactions capture energy as ATP and NADPH in the thylakoid membranes of the chloroplast.</w:t></w:r></w:p>`
	row := `<w:tr>` + strings.Repeat(`<w:tc><w:tcPr><w:tcW w:w="3000" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>Cell</w:t></w:r></w:p></w:tc>`, 3) + `</w:tr>`
	// About twenty pages: four hundred paragraphs and a table of sixty rows.
	file := docx(b, strings.Repeat(paragraph, 400)+`<w:tbl><w:tblGrid><w:gridCol w:w="3000"/></w:tblGrid>`+strings.Repeat(row, 60)+`</w:tbl>`+wordSection)
	b.ReportAllocs()
	for b.Loop() {
		if got := checkDOCX(file); len(got.findings) != 0 {
			b.Fatal(got.findings)
		}
	}
}

func BenchmarkCheckPPTX(b *testing.B) {
	slide := strings.Repeat(shape(100, 100, 5000, 3000), 12)
	slides := make([]string, 30)
	for i := range slides {
		slides[i] = slide
	}
	file := pptx(b, "", slides...)
	b.ReportAllocs()
	for b.Loop() {
		if got := checkPPTX(file); len(got.findings) != 0 || got.pages != 30 {
			b.Fatal(got)
		}
	}
}

func BenchmarkCheckXLSX(b *testing.B) {
	// Ten thousand cells, a tenth of them formulas.
	var cells strings.Builder
	for i := range 10_000 {
		if i%10 == 0 {
			cells.WriteString(`<c><f>A1*2</f><v>8</v></c>`)
		} else {
			cells.WriteString(`<c><v>4</v></c>`)
		}
	}
	file := xlsx(b, cells.String())
	b.ReportAllocs()
	for b.Loop() {
		if got := checkXLSX(file); len(got.findings) != 0 {
			b.Fatal(got.findings)
		}
	}
}
