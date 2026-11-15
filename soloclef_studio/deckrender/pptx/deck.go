package pptx

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// template is python-pptx's default template (via Tools), set to 16:9,
// less its printer settings, thumbnail and author: the master, the
// layouts, the theme and the notes master a deck refers to.
//
//go:embed all:template
var template embed.FS

// The slide: 13.333 by 7.5 inches, PowerPoint's widescreen.
const (
	SlideW int64 = 12192000
	SlideH int64 = 6858000
)

const (
	ctBase  = "application/vnd.openxmlformats-officedocument."
	ctSlide = ctBase + "presentationml.slide+xml"
	ctNotes = ctBase + "presentationml.notesSlide+xml"
)

// Presentation is one deck being written.
type Presentation struct {
	title    string
	created  time.Time
	slides   []*Slide
	presRels []rel
	hasNotes bool
	notesN   int
}

// New starts a deck. title goes into the file's properties, which is what
// a file manager and PowerPoint's info pane show; created is its creation
// time (passed in so the same deck written twice is the same bytes).
func New(title string, created time.Time) *Presentation {
	p := &Presentation{title: title, created: created.UTC()}
	for i, t := range []struct{ typ, target string }{
		{"slideMaster", "slideMasters/slideMaster1.xml"}, {"presProps", "presProps.xml"},
		{"viewProps", "viewProps.xml"}, {"theme", "theme/theme1.xml"}, {"tableStyles", "tableStyles.xml"},
	} {
		p.presRels = append(p.presRels, rel{id: fmt.Sprintf("rId%d", i+1), typ: relBase + t.typ, target: t.target})
	}
	return p
}

func (p *Presentation) relate(typ, target string) string {
	id := fmt.Sprintf("rId%d", len(p.presRels)+1)
	p.presRels = append(p.presRels, rel{id: id, typ: relBase + typ, target: target})
	return id
}

// Slide is one slide on the blank layout.
type Slide struct {
	n          int
	presRID    string
	elements   []element
	nextID     int
	rels       []rel
	background string
	notes      string
	notesN     int
	transition string
	name       string
}

type element interface{ write(b *strings.Builder) }

// AddSlide adds a blank slide.
func (p *Presentation) AddSlide() *Slide {
	n := len(p.slides) + 1
	s := &Slide{n: n, nextID: 2}
	s.presRID = p.relate("slide", fmt.Sprintf("slides/slide%d.xml", n))
	s.rels = []rel{{id: "rId1", typ: relBase + "slideLayout", target: "../slideLayouts/slideLayout7.xml"}}
	p.slides = append(p.slides, s)
	return s
}

// SetName names the slide (p:cSld name): PowerPoint does not show it; a
// reader of the file, and the layout check, can.
func (s *Slide) SetName(name string) { s.name = name }

// SetBackground fills the slide with one colour (six hex digits). It is
// the slide's own background, not a rectangle, so nothing on the slide
// can be "hidden under" it.
func (s *Slide) SetBackground(hex string) { s.background = hex }

// SetTransition sets how the slide comes in: "fade" or "push" or "" for a
// cut.
func (s *Slide) SetTransition(kind string) { s.transition = kind }

// SetNotes gives the slide speaker notes, a paragraph per line.
func (s *Slide) SetNotes(text string, p *Presentation) {
	if text == "" {
		return
	}
	if s.notesN == 0 {
		if !p.hasNotes {
			p.hasNotes = true
			p.relate("notesMaster", "notesMasters/notesMaster1.xml")
		}
		p.notesN++
		s.notesN = p.notesN
		s.rels = append(s.rels, rel{id: fmt.Sprintf("rId%d", len(s.rels)+1), typ: relBase + "notesSlide", target: fmt.Sprintf("../notesSlides/notesSlide%d.xml", s.notesN)})
	}
	s.notes = text
}

func (s *Slide) take() int { id := s.nextID; s.nextID++; return id }

const namespaces = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

func (s *Slide) xml() []byte {
	var b strings.Builder
	b.WriteString(xmlHead + `<p:sld ` + namespaces + `><p:cSld`)
	if s.name != "" {
		b.WriteString(` name="` + escapeAttr(s.name) + `"`)
	}
	b.WriteString(`>`)
	if s.background != "" {
		b.WriteString(`<p:bg><p:bgPr><a:solidFill><a:srgbClr val="` + s.background + `"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>`)
	}
	b.WriteString(`<p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>`)
	for _, e := range s.elements {
		e.write(&b)
	}
	b.WriteString(`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>`)
	switch s.transition {
	case "fade":
		b.WriteString(`<p:transition spd="med"><p:fade/></p:transition>`)
	case "push":
		b.WriteString(`<p:transition spd="med"><p:push dir="u"/></p:transition>`)
	}
	b.WriteString(`</p:sld>`)
	return []byte(b.String())
}

func notesXML(text string) []byte {
	var b strings.Builder
	b.WriteString(xmlHead + `<p:notes ` + namespaces + `><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr><p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldImg" idx="2"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp><p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="3" sz="quarter"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>`)
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			b.WriteString("<a:p/>")
			continue
		}
		b.WriteString("<a:p><a:r><a:t>" + escapeText(line) + "</a:t></a:r></a:p>")
	}
	b.WriteString(`</p:txBody></p:sp></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`)
	return []byte(b.String())
}

// notesOnly are the template's parts a deck carries only when it has notes.
var notesOnly = map[string]bool{
	"ppt/notesMasters/notesMaster1.xml":            true,
	"ppt/notesMasters/_rels/notesMaster1.xml.rels": true,
	"ppt/theme/theme2.xml":                         true,
}

var templateTypes = map[string]string{
	"docProps/app.xml":                  ctBase + "extended-properties+xml",
	"ppt/presProps.xml":                 ctBase + "presentationml.presProps+xml",
	"ppt/viewProps.xml":                 ctBase + "presentationml.viewProps+xml",
	"ppt/tableStyles.xml":               ctBase + "presentationml.tableStyles+xml",
	"ppt/theme/theme1.xml":              ctBase + "theme+xml",
	"ppt/theme/theme2.xml":              ctBase + "theme+xml",
	"ppt/slideMasters/slideMaster1.xml": ctBase + "presentationml.slideMaster+xml",
	"ppt/notesMasters/notesMaster1.xml": ctBase + "presentationml.notesMaster+xml",
}

func (p *Presentation) coreXML() []byte {
	at := p.created.Format("2006-01-02T15:04:05Z")
	return []byte(xmlHead + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` +
		escapeText(p.title) + `</dc:title><dc:creator>Soloclef</dc:creator><cp:revision>1</cp:revision><dcterms:created xsi:type="dcterms:W3CDTF">` + at +
		`</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + at + `</dcterms:modified></cp:coreProperties>`)
}

// parts is every part of the deck, the package's relationships first.
func (p *Presentation) parts() ([]part, error) {
	parts := []part{}
	var packageRels []byte
	err := fs.WalkDir(template, "template", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(name, "template/")
		if rel == "ppt/presentation.xml" || (notesOnly[rel] && !p.hasNotes) {
			return nil
		}
		data, err := template.ReadFile(name)
		if err != nil {
			return err
		}
		if rel == "_rels/.rels" {
			packageRels = data
			return nil
		}
		contentType := templateTypes[rel]
		if strings.HasPrefix(rel, "ppt/slideLayouts/slideLayout") && strings.HasSuffix(rel, ".xml") {
			contentType = ctBase + "presentationml.slideLayout+xml"
		}
		parts = append(parts, part{name: rel, contentType: contentType, data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	presentation, err := template.ReadFile("template/ppt/presentation.xml")
	if err != nil {
		return nil, err
	}
	var ids strings.Builder
	if len(p.slides) > 0 {
		ids.WriteString("<p:sldIdLst>")
		for i, s := range p.slides {
			fmt.Fprintf(&ids, `<p:sldId id="%d" r:id="%s"/>`, 256+i, s.presRID)
		}
		ids.WriteString("</p:sldIdLst>")
	}
	text := string(presentation)
	from, to := strings.Index(text, "<p:sldIdLst>"), strings.Index(text, "</p:sldIdLst>")
	if from < 0 || to < 0 {
		return nil, fmt.Errorf("pptx template: presentation.xml has no sldIdLst")
	}
	parts = append(parts,
		part{name: "docProps/core.xml", contentType: "application/vnd.openxmlformats-package.core-properties+xml", data: p.coreXML()},
		part{name: "ppt/presentation.xml", contentType: ctBase + "presentationml.presentation.main+xml", data: []byte(text[:from] + ids.String() + text[to+len("</p:sldIdLst>"):])},
		part{name: "ppt/_rels/presentation.xml.rels", data: relsXML(p.presRels)})
	for _, s := range p.slides {
		parts = append(parts,
			part{name: fmt.Sprintf("ppt/slides/slide%d.xml", s.n), contentType: ctSlide, data: s.xml()},
			part{name: fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", s.n), data: relsXML(s.rels)})
		if s.notesN > 0 {
			parts = append(parts,
				part{name: fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", s.notesN), contentType: ctNotes, data: notesXML(s.notes)},
				part{name: fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", s.notesN), data: relsXML([]rel{
					{id: "rId1", typ: relBase + "notesMaster", target: "../notesMasters/notesMaster1.xml"},
					{id: "rId2", typ: relBase + "slide", target: fmt.Sprintf("../slides/slide%d.xml", s.n)}})})
		}
	}
	return append([]part{{name: "_rels/.rels", data: packageRels}}, parts...), nil
}

// Bytes is the finished .pptx.
func (p *Presentation) Bytes() ([]byte, error) {
	parts, err := p.parts()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writePackage(&buf, parts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
