package deliverables

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The live deck "Apollo 11 in brief" (made by the Go pipeline, 2026-11-12)
// and the words LibreOffice drew for it (pdftotext -bbox-layout in the
// sandbox image). A model's look passed it; on slide 1 a gold staircase line
// runs through the footer's source text and through the label "the moon".
// Slides 2 to 6 are laid out cleanly, but their source line and page number
// are set at 11 pt, under the 12 pt floor Tools holds its own decks to.
func TestTheLiveApolloDeck(t *testing.T) {
	deck, pages := apollo(t)
	if len(deck.slides) != 6 || len(pages) != 6 {
		t.Fatalf("%d slides, %d pages", len(deck.slides), len(pages))
	}
	findings := layoutFindings(pages, &deck)
	want := []string{
		`slide 1: the line "Freeform 5" crosses the text of "TextBox 4" ("Compiled from President Kennedy's Special Message to Cong...")`,
		`slide 1: the line "Freeform 5" crosses the text of "TextBox 7" ("the moon")`,
	}
	for slide := 2; slide <= 6; slide++ {
		want = append(want,
			fmt.Sprintf(`slide %d: "Sources: Kennedy, Special Message to Congress (25 May 196..." in TextBox `, slide),
			fmt.Sprintf(`slide %d: "%d" in TextBox `, slide, slide))
	}
	if len(findings) != len(want) {
		t.Fatalf("findings:\n%s", strings.Join(findings, "\n"))
	}
	for i, finding := range findings {
		if !strings.HasPrefix(finding, want[i]) || i >= 2 && !strings.Contains(finding, "is set at 11 pt; nothing on a slide may be under 12 pt") {
			t.Errorf("finding %d:\n got %s\nwant %s...", i, finding, want[i])
		}
	}
}

func apollo(t testing.TB) (deckShapes, []pdfPage) {
	t.Helper()
	data, err := os.ReadFile("testdata/apollo-deck.pptx")
	if err != nil {
		t.Fatal(err)
	}
	if checked := checkPPTX(data); len(checked.findings) != 0 {
		t.Fatalf("the deck's structure: %q", checked.findings)
	}
	deck, err := readDeckShapes(data)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := os.ReadFile("testdata/apollo-deck.words.html")
	if err != nil {
		t.Fatal(err)
	}
	pages, err := readPDFWords(listed)
	if err != nil {
		t.Fatal(err)
	}
	return deck, pages
}

// What is read back of the deck: each frame's size, colour and ground
// resolved as the slide draws them.
func TestTheDeckIsReadAsItIsDrawn(t *testing.T) {
	deck, _ := apollo(t)
	if deck.width < 959.9 || deck.width > 960 || deck.height != 540 {
		t.Errorf("slide size %g by %g", deck.width, deck.height)
	}
	first := deck.slides[0]
	byName := map[string]textFrame{}
	for _, f := range first.frames {
		byName[f.name] = f
	}
	title := byName["Title 1"]
	if len(title.runs) != 1 || title.runs[0].size != 54 || title.runs[0].colour != "F2EFE9" || title.runs[0].ground != "0A1A28" {
		t.Errorf("the title: %+v", title)
	}
	if footer := byName["TextBox 4"]; !footer.grows || footer.runs[0].size != 12 {
		t.Errorf("the footer: %+v", footer)
	}
	if len(first.strokes) != 1 || first.strokes[0].name != "Freeform 5" || len(first.strokes[0].segments) == 0 {
		t.Errorf("the strokes: %+v", first.strokes)
	}
	if len(first.covers) != 1 || first.covers[0].name != "Oval 6" {
		t.Errorf("the covers: %+v", first.covers)
	}
}

// A deck written here, shape by shape, and the words as LibreOffice would
// draw them: each rule on its own.
func TestEachPlacementRule(t *testing.T) {
	const emu = 12700
	box := func(name string, x, y, w, h int, body string) string {
		return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"/><a:noFill/></p:spPr>%s</p:sp>`,
			name, x*emu, y*emu, w*emu, h*emu, body)
	}
	text := func(size int, colour string, paragraphs ...string) string {
		var b strings.Builder
		b.WriteString(`<p:txBody><a:bodyPr wrap="square"/><a:lstStyle/>`)
		for _, p := range paragraphs {
			fmt.Fprintf(&b, `<a:p><a:r><a:rPr sz="%d"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:rPr><a:t>%s</a:t></a:r></a:p>`, size*100, colour, p)
		}
		b.WriteString(`</p:txBody>`)
		return b.String()
	}
	filled := func(name string, x, y, w, h int) string {
		return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="3" name="%s"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"/><a:solidFill><a:srgbClr val="EEF2F7"/></a:solidFill></p:spPr></p:sp>`,
			name, x*emu, y*emu, w*emu, h*emu)
	}
	connector := func(name string, x, y, w, h int) string {
		return fmt.Sprintf(`<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="4" name="%s"/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="straightConnector1"/><a:ln w="12700"><a:solidFill><a:srgbClr val="000000"/></a:solidFill></a:ln></p:spPr></p:cxnSp>`,
			name, x*emu, y*emu, w*emu, h*emu)
	}
	picture := func(name string, x, y, w, h int) string {
		return fmt.Sprintf(`<p:pic><p:nvPicPr><p:cNvPr id="5" name="%s"/><p:cNvPicPr/><p:nvPr/></p:nvPicPr><p:blipFill/><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm></p:spPr></p:pic>`,
			name, x*emu, y*emu, w*emu, h*emu)
	}
	word := func(x, y, w, h float64, s string) string {
		return fmt.Sprintf(`<word xMin="%g" yMin="%g" xMax="%g" yMax="%g">%s</word>`, x, y, x+w, y+h, s)
	}
	line := func(words ...string) string { return "<line>" + strings.Join(words, "") + "</line>" }
	page := func(lines ...string) string {
		return `<doc><page width="960" height="540"><flow><block>` + strings.Join(lines, "") + `</block></flow></page></doc>`
	}
	// The heading "Cell biology" in a 300 by 60 pt box at (60, 40), drawn
	// where it belongs.
	heading := box("Heading", 60, 40, 300, 60, text(32, "111111", "Cell biology"))
	headingDrawn := line(word(60, 45, 70, 36, "Cell"), word(136, 45, 110, 36, "biology"))

	for name, test := range map[string]struct {
		shapes, words string
		want          string
	}{
		"a clean slide": {heading, page(headingDrawn), ""},
		"text that runs out of its box": {
			heading, page(line(word(60, 45, 70, 36, "Cell")), line(word(60, 110, 110, 36, "biology"))),
			`slide 1: the text of "Heading" ("Cell biology") is not drawn whole inside its box: 1 of its 2 words are outside it or broken, among them "biology"`},
		"two boxes' text drawn over each other": {
			heading + box("Subtitle", 60, 60, 300, 60, text(24, "111111", "Mitosis")),
			page(headingDrawn, line(word(70, 50, 90, 28, "Mitosis"))),
			`slide 1: the text of "Heading" ("Cell biology") and the text of "Subtitle" ("Mitosis") are drawn over each other`},
		"a line through text": {
			heading + connector("Arrow", 100, 20, 0, 100), page(headingDrawn),
			`slide 1: the line "Arrow" crosses the text of "Heading" ("Cell biology")`},
		"a line that only points at text": {heading + connector("Arrow", 400, 63, 100, 0), page(headingDrawn), ""},
		"text under a shape drawn after it": {
			heading + filled("Card", 50, 30, 200, 80), page(headingDrawn),
			`slide 1: the text of "Heading" ("Cell biology") is hidden under the shape "Card" drawn over it`},
		"text on a card drawn before it": {filled("Card", 50, 30, 320, 80) + heading, page(headingDrawn), ""},
		"a picture off the slide": {
			heading + picture("Photo", 700, 300, 400, 200), page(headingDrawn),
			`slide 1: the picture "Photo" runs off the slide`},
		"a background picture": {picture("Backdrop", 0, 0, 960, 540) + heading, page(headingDrawn), ""},
		"text too small": {
			box("Note", 60, 400, 300, 30, text(10, "111111", "Footnote")), page(line(word(60, 405, 50, 11, "Footnote"))),
			`slide 1: "Footnote" in Note is set at 10 pt; nothing on a slide may be under 12 pt`},
		"a small body": {
			box("Body", 60, 100, 600, 300, text(14, "111111", "One", "Two", "Three")),
			page(line(word(60, 105, 40, 16, "One")), line(word(60, 130, 40, 16, "Two")), line(word(60, 155, 50, 16, "Three"))),
			`slide 1: the body text of "Body" ("One Two Three") is set under 16 pt`},
		"grey on white": {
			box("Body", 60, 100, 600, 60, text(20, "BBBBBB", "Faint")), page(line(word(60, 105, 60, 23, "Faint"))),
			`slide 1: "Faint" in Body is #BBBBBB on #FFFFFF at 20 pt: contrast 1.92:1, and it needs 4.5:1`},
		"large text at three to one": {
			box("Body", 60, 100, 600, 60, text(28, "888888", "Large")), page(line(word(60, 105, 80, 32, "Large"))), ""},
		"words over each other in one box": {
			box("Body", 60, 100, 600, 60, text(20, "111111", "Alpha Beta")), page(line(word(60, 105, 60, 23, "Alpha"), word(70, 105, 50, 23, "Beta"))),
			`slide 1: words are drawn over each other: "Alpha / Beta"`},
		"text off the slide": {
			box("Wide", 800, 100, 300, 60, text(20, "111111", "Edge")), page(line(word(940, 105, 60, 23, "Edge"))),
			`slide 1: text runs off the slide and is cut: "Edge"`},
	} {
		data := zipOf(t, map[string]string{
			"[Content_Types].xml": "<Types/>", "_rels/.rels": "<Relationships/>",
			"ppt/presentation.xml":            `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/></p:presentation>`,
			"ppt/_rels/presentation.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/></Relationships>`,
			"ppt/slides/slide1.xml": slideOpen + `><p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></p:bgPr></p:bg><p:spTree>` +
				test.shapes + `</p:spTree></p:cSld></p:sld>`,
		})
		deck, err := readDeckShapes(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pages, err := readPDFWords([]byte(test.words))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		findings := layoutFindings(pages, &deck)
		switch {
		case test.want == "" && len(findings) != 0:
			t.Errorf("%s: %q", name, findings)
		case test.want != "" && (len(findings) != 1 || !strings.HasPrefix(findings[0], test.want)):
			t.Errorf("%s:\n got %q\nwant %q...", name, findings, test.want)
		}
	}
}

// A report's pages are held to their edges and to words not overlapping.
func TestAReportsPages(t *testing.T) {
	pages, err := readPDFWords([]byte(`<doc><page width="595" height="842"><flow><block><line>` +
		`<word xMin="72" yMin="72" xMax="120" yMax="84">Fine</word><word xMin="560" yMin="90" xMax="610" yMax="102">Clipped</word>` +
		`</line><line><word xMin="72" yMin="200" xMax="120" yMax="212">Over</word><word xMin="75" yMin="201" xMax="118" yMax="213">Lapped</word></line></block></flow></page></doc>`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`page 1: text runs off the page and is cut: "Clipped"`, `page 1: words are drawn over each other: "Over / Lapped"`}
	if got := layoutFindings(pages, nil); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("findings %q", got)
	}
}

// The colour arithmetic, against WCAG's own figures.
func TestContrastAndColours(t *testing.T) {
	if got := contrast("000000", "FFFFFF"); got < 20.99 || got > 21.01 {
		t.Errorf("black on white %.3f", got)
	}
	if got := contrast("777777", "FFFFFF"); got < 4.47 || got > 4.49 {
		t.Errorf("#777 on white %.3f", got)
	}
	if got := adjustLightness("4472C4", "lumMod", 0.75); got != "2F5597" {
		t.Errorf("accent 1 darker 25%%: %s", got)
	}
	if got := blend("FFFFFF", "000000", 0.5); got != "808080" {
		t.Errorf("half black on white: %s", got)
	}
}

// A markdown representation that is a way to get the text, not the text.
func TestAnUnexpandedRepresentationIsRefused(t *testing.T) {
	for value, refused := range map[string]bool{
		"$(cat /workspace/md_rep.md)":        true,
		"  $(cat /workspace/md_rep.md)\n":    true,
		"`cat /workspace/deck.md`":           true,
		"$MARKDOWN":                          true,
		"${md}":                              true,
		"/workspace/md_rep.md":               true,
		"# Apollo 11\n\nRun $(cat x) to see": false,
		"# Shell\n\n`ls` lists files.":       false,
		"Costs $5":                           false,
	} {
		if unexpanded(value) != refused {
			t.Errorf("%q: refused %v", value, !refused)
		}
	}
	// Through the tool: refused, saying why, and nothing is stored.
	_, l, tools := built(t, "/workspace/deck.pptx", nil)
	_, err := runTool(t, tools[SaveTool], `{"format":"pptx","title":"Apollo 11","path":"/workspace/deck.pptx","markdown_representation":"$(cat /workspace/md_rep.md)"}`)
	if err == nil || !strings.Contains(err.Error(), "a shell expression or a file path") || len(l.created) != 0 {
		t.Errorf("%v, stored %d", err, len(l.created))
	}
}

// The rules' cost on the six-slide deck: reading it back and measuring.
func BenchmarkPlacementRules(b *testing.B) {
	data, err := os.ReadFile("testdata/apollo-deck.pptx")
	if err != nil {
		b.Fatal(err)
	}
	listed, err := os.ReadFile("testdata/apollo-deck.words.html")
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		deck, err := readDeckShapes(data)
		if err != nil {
			b.Fatal(err)
		}
		pages, err := readPDFWords(listed)
		if err != nil {
			b.Fatal(err)
		}
		layoutFindings(pages, &deck)
	}
}
