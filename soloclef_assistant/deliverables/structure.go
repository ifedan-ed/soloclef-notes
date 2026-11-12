package deliverables

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// The structural checks: what can be said about a built file from its bytes
// alone, before anything is rendered. They catch the faults that a picture
// of the page does not show (a formula with no cached value, a table Word
// will lay out differently, a slide that is hidden) and the ones that would
// make rendering pointless (not a document at all).
//
// They run in this process, on bytes read out of the sandbox, and never in
// the sandbox: the file was made by code a model wrote, and a check run
// beside that code could be answered by it.

// structure is what a structural check found.
type structure struct {
	// findings are faults that block the save.
	findings []string
	// notes are told to the model and shown on the card; they block
	// nothing.
	notes []string
	// pages is the number of slides, for a deck; 0 when the format has no
	// such count before rendering.
	pages int
}

func (s *structure) find(format string, args ...any) {
	s.findings = append(s.findings, fmt.Sprintf(format, args...))
}

// The bounds on an Office file read as a ZIP. The file is untrusted: a small
// archive can declare parts that unpack to gigabytes.
const (
	maxZipEntries        = 10_000
	maxUncompressedBytes = 100 << 20
	maxXMLPartBytes      = 10 << 20
	maxWorkbookXMLBytes  = 2 << 20
	maxSheetCells        = 100_000
)

// office is an Office file opened as its ZIP of parts.
type office struct {
	name  string
	parts map[string]*zip.File
}

// errOffice is a fault of the package itself; its text is the finding.
type errOffice struct{ finding string }

func (e *errOffice) Error() string { return e.finding }

// openOffice opens an Office file and holds it to the package-level bounds.
func openOffice(data []byte, name string, required ...string) (*office, error) {
	invalid := &errOffice{name + " is not valid OOXML"}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid
	}
	if len(archive.File) > maxZipEntries {
		return nil, &errOffice{fmt.Sprintf("%s contains more than %d ZIP entries", name, maxZipEntries)}
	}
	parts := make(map[string]*zip.File, len(archive.File))
	var total uint64
	for _, file := range archive.File {
		if _, twice := parts[file.Name]; twice {
			return nil, &errOffice{name + " contains duplicate OOXML parts"}
		}
		if file.Flags&1 != 0 {
			return nil, &errOffice{name + " contains encrypted ZIP entries"}
		}
		parts[file.Name] = file
		total += file.UncompressedSize64
	}
	if total > maxUncompressedBytes {
		return nil, &errOffice{fmt.Sprintf("%s uncompressed content exceeds %d bytes", name, maxUncompressedBytes)}
	}
	var missing []string
	for _, part := range required {
		if parts[part] == nil {
			missing = append(missing, part)
		}
	}
	if len(missing) > 0 {
		return nil, &errOffice{name + " is missing required parts: " + strings.Join(missing, ", ")}
	}
	return &office{name: name, parts: parts}, nil
}

// read returns one part's bytes, at most limit of them. The declared size
// is not trusted: the read itself is bounded.
func (o *office) read(part string, limit int64) ([]byte, error) {
	file := o.parts[part]
	if file == nil {
		return nil, &errOffice{o.name + " is missing the part " + part}
	}
	reader, err := file.Open()
	if err != nil {
		return nil, &errOffice{o.name + " is not valid OOXML"}
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, &errOffice{o.name + " is not valid OOXML"}
	}
	if int64(len(data)) > limit {
		return nil, &errOffice{fmt.Sprintf("%s part %s exceeds %d bytes", o.name, part, limit)}
	}
	return data, nil
}

// element is one XML element as a tree: enough of a document's structure for
// these checks, which ask "is there a child called X" and "what is its
// attribute Y" and never need the text's order across elements.
type element struct {
	space, name string
	attrs       []xml.Attr
	children    []*element
	text        string
}

// parseXML reads a part into a tree. maxNodes bounds it.
func parseXML(data []byte) (*element, error) {
	const maxNodes = 2_000_000
	decoder := xml.NewDecoder(bytes.NewReader(data))
	// Office parts are UTF-8 or UTF-16 with a declaration; anything else
	// is not a document these checks should read.
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(label, "utf-8") || strings.EqualFold(label, "us-ascii") {
			return input, nil
		}
		return nil, errors.New("unsupported encoding " + label)
	}
	var root *element
	var stack []*element
	nodes := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			if nodes++; nodes > maxNodes {
				return nil, errors.New("too many elements")
			}
			node := &element{space: t.Name.Space, name: t.Name.Local, attrs: t.Attr}
			if len(stack) == 0 {
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("no root element")
	}
	return root, nil
}

// attr is an attribute by namespace and name; space "" matches an
// attribute with no namespace.
func (e *element) attr(space, name string) (string, bool) {
	for _, a := range e.attrs {
		if a.Name.Local == name && a.Name.Space == space {
			return a.Value, true
		}
	}
	return "", false
}

// child is the first direct child of that name, or nil.
func (e *element) child(space, name string) *element {
	if e == nil {
		return nil
	}
	for _, c := range e.children {
		if c.name == name && c.space == space {
			return c
		}
	}
	return nil
}

// each calls visit for every descendant of that name, in document order.
func (e *element) each(space, name string, visit func(*element)) {
	for _, c := range e.children {
		if c.name == name && c.space == space {
			visit(c)
		}
		c.each(space, name, visit)
	}
}

// has reports whether any descendant has that name.
func (e *element) has(space, name string) bool {
	found := false
	e.each(space, name, func(*element) { found = true })
	return found
}

// The XML namespaces of the three Office formats.
const (
	wordNS     = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	slidesNS   = "http://schemas.openxmlformats.org/presentationml/2006/main"
	drawingNS  = "http://schemas.openxmlformats.org/drawingml/2006/main"
	sheetNS    = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	officeRel  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	packageRel = "http://schemas.openxmlformats.org/package/2006/relationships"
)

// The margins a Word section may have, in twentieths of a point: half an
// inch to two inches. Outside that a page prints clipped or mostly empty.
const (
	minMarginTwips = 720
	maxMarginTwips = 2880
)

// checkDOCX checks a Word document: that it has text and page setup, and
// that its tables, shading and lists are written the way Word and
// LibreOffice both lay out the same.
func checkDOCX(data []byte) structure {
	var out structure
	if len(data) == 0 {
		out.find("DOCX is empty")
		return out
	}
	archive, err := openOffice(data, "DOCX", "[Content_Types].xml", "_rels/.rels", "word/document.xml")
	if err != nil {
		out.find("%s", err)
		return out
	}
	raw, err := archive.read("word/document.xml", maxXMLPartBytes)
	if err != nil {
		out.find("%s", err)
		return out
	}
	root, err := parseXML(raw)
	if err != nil {
		out.find("DOCX is not valid OOXML")
		return out
	}

	var text strings.Builder
	root.each(wordNS, "t", func(t *element) { text.WriteString(t.text) })
	if root.child(wordNS, "body") == nil || strings.TrimSpace(text.String()) == "" {
		out.find("DOCX body contains no text")
	}

	section := 0
	root.each(wordNS, "sectPr", func(sect *element) {
		section++
		margins := sect.child(wordNS, "pgMar")
		if margins == nil {
			out.find("section %d has no page margins", section)
			return
		}
		for _, side := range []string{"top", "right", "bottom", "left"} {
			written, present := margins.attr(wordNS, side)
			value, _ := strconv.Atoi(written)
			if value < minMarginTwips || value > maxMarginTwips {
				if !present {
					written = "missing"
				}
				out.find("section %d has an unsafe %s margin (%s twips)", section, side, written)
			}
		}
	})
	if section == 0 {
		out.find("DOCX has no section page setup")
	}

	root.each(wordNS, "tblW", func(width *element) {
		if kind, _ := width.attr(wordNS, "type"); kind == "pct" {
			out.find("table uses percentage width; use DXA widths")
		}
	})
	root.each(wordNS, "tc", func(cell *element) {
		width := cell.child(wordNS, "tcPr").child(wordNS, "tcW")
		kind, size := "", 0
		if width != nil {
			kind, _ = width.attr(wordNS, "type")
			written, _ := width.attr(wordNS, "w")
			size, _ = strconv.Atoi(written)
		}
		if kind != "dxa" || size <= 0 {
			out.find("table cell is missing a positive DXA width")
		}
	})
	root.each(wordNS, "tbl", func(table *element) {
		if grid := table.child(wordNS, "tblGrid"); grid == nil || grid.child(wordNS, "gridCol") == nil {
			out.find("table is missing w:tblGrid column widths")
		}
	})
	solid := false
	root.each(wordNS, "shd", func(shading *element) {
		if value, _ := shading.attr(wordNS, "val"); value == "solid" {
			solid = true
		}
	})
	if solid {
		out.find("shading uses solid; use clear shading")
	}
	root.each(wordNS, "p", func(paragraph *element) {
		var line strings.Builder
		paragraph.each(wordNS, "t", func(t *element) { line.WriteString(t.text) })
		if strings.HasPrefix(strings.TrimLeft(line.String(), " \t\r\n"), "•") && paragraph.child(wordNS, "pPr").child(wordNS, "numPr") == nil {
			out.find("paragraph uses a literal bullet instead of numbering")
		}
	})

	contents := false
	isContents := func(instruction string) bool {
		return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(instruction)), "TOC")
	}
	root.each(wordNS, "instrText", func(e *element) { contents = contents || isContents(e.text) })
	root.each(wordNS, "fldSimple", func(e *element) {
		instruction, _ := e.attr(wordNS, "instr")
		contents = contents || isContents(instruction)
	})
	if contents {
		out.notes = append(out.notes, "The document contains a TOC field; page numbers populate when Word opens and updates fields.")
	}
	out.findings = distinct(out.findings)
	return out
}

// relationship is one entry of a part's relationships.
type relationship struct {
	target, kind string
	external     bool
}

// relationships reads a part's relationships; a part with none has no file.
func (o *office) relationships(relsPart, sourcePart string) (map[string]relationship, error) {
	if o.parts[relsPart] == nil {
		return map[string]relationship{}, nil
	}
	raw, err := o.read(relsPart, maxXMLPartBytes)
	if err != nil {
		return nil, err
	}
	root, err := parseXML(raw)
	if err != nil {
		return nil, &errOffice{o.name + " is not valid OOXML"}
	}
	out := map[string]relationship{}
	for _, entry := range root.children {
		if entry.name != "Relationship" || entry.space != packageRel {
			continue
		}
		id, _ := entry.attr("", "Id")
		target, _ := entry.attr("", "Target")
		if id == "" || target == "" {
			continue
		}
		kind, _ := entry.attr("", "Type")
		mode, _ := entry.attr("", "TargetMode")
		// A target is relative to the part that names it, unless it
		// starts at the package's root (openpyxl writes its sheets as
		// "/xl/worksheets/sheet1.xml").
		resolved := strings.TrimPrefix(path.Clean(target), "/")
		if !strings.HasPrefix(target, "/") {
			resolved = path.Clean(path.Join(path.Dir(sourcePart), target))
		}
		out[id] = relationship{target: resolved, kind: kind, external: mode == "External"}
	}
	return out, nil
}

// drawable are the children of a slide's shape tree that put something on
// the slide.
var drawable = map[string]bool{"sp": true, "pic": true, "graphicFrame": true, "cxnSp": true, "grpSp": true, "contentPart": true}

// checkPPTX checks a deck: that every slide exists, shows something, is not
// hidden, and has no shape wholly off the canvas or picture cropped away.
// How the text sits on a slide is measured once it is drawn
// (layout_rules.go).
func checkPPTX(data []byte) structure {
	var out structure
	if len(data) == 0 {
		out.find("PPTX is empty")
		return out
	}
	const presentation, presentationRels = "ppt/presentation.xml", "ppt/_rels/presentation.xml.rels"
	archive, err := openOffice(data, "PPTX", "[Content_Types].xml", "_rels/.rels", presentation, presentationRels)
	if err != nil {
		out.find("%s", err)
		return out
	}
	fail := func(err error) structure {
		var fault *errOffice
		if errors.As(err, &fault) {
			out.find("%s", fault)
		} else {
			out.find("PPTX is not valid OOXML")
		}
		return out
	}
	raw, err := archive.read(presentation, maxXMLPartBytes)
	if err != nil {
		return fail(err)
	}
	root, err := parseXML(raw)
	if err != nil {
		return fail(err)
	}
	width, height := 0, 0
	if size := root.child(slidesNS, "sldSz"); size != nil {
		cx, _ := size.attr("", "cx")
		cy, _ := size.attr("", "cy")
		width, _ = strconv.Atoi(cx)
		height, _ = strconv.Atoi(cy)
	}
	if width <= 0 || height <= 0 {
		out.find("PPTX has no positive presentation-wide slide size")
		return out
	}
	var slides []*element
	if list := root.child(slidesNS, "sldIdLst"); list != nil {
		for _, entry := range list.children {
			if entry.name == "sldId" && entry.space == slidesNS {
				slides = append(slides, entry)
			}
		}
	}
	if len(slides) == 0 {
		out.find("PPTX contains no slides")
		return out
	}
	out.pages = len(slides)
	links, err := archive.relationships(presentationRels, presentation)
	if err != nil {
		return fail(err)
	}
	for i, slide := range slides {
		number := i + 1
		id, _ := slide.attr(officeRel, "id")
		link, found := links[id]
		if !found || link.external || !strings.HasSuffix(link.kind, "/slide") || archive.parts[link.target] == nil {
			out.find("slide %d points to a missing slide part", number)
			continue
		}
		if err := checkSlide(archive, link.target, number, width, height, &out); err != nil {
			return fail(err)
		}
	}
	out.findings = distinct(out.findings)
	return out
}

func checkSlide(archive *office, part string, number, slideWidth, slideHeight int, out *structure) error {
	raw, err := archive.read(part, maxXMLPartBytes)
	if err != nil {
		return err
	}
	slide, err := parseXML(raw)
	if err != nil {
		return err
	}
	if shown, _ := slide.attr("", "show"); shown == "0" {
		out.find("slide %d is hidden; delete it so every delivered slide is rendered and verified", number)
	}
	shapes := 0
	if tree := slide.child(slidesNS, "cSld").child(slidesNS, "spTree"); tree != nil {
		for _, shape := range tree.children {
			if shape.space != slidesNS || !drawable[shape.name] {
				continue
			}
			shapes++
			checkShape(shape, number, slideWidth, slideHeight, out)
		}
	}
	if shapes == 0 {
		out.find("slide %d contains no drawable shapes", number)
	}

	slide.each(drawingNS, "srcRect", func(crop *element) {
		sides := map[string]int{}
		for _, side := range []string{"l", "t", "r", "b"} {
			written, present := crop.attr("", side)
			if !present {
				continue
			}
			value, err := strconv.Atoi(written)
			if err != nil {
				out.find("slide %d has an invalid picture crop", number)
				return
			}
			sides[side] = value
		}
		// Crops are in thousandths of a percent of the picture.
		if sides["l"]+sides["r"] >= 100_000 || sides["t"]+sides["b"] >= 100_000 {
			out.find("slide %d has a picture crop that removes the entire image", number)
		}
	})

	links, err := archive.relationships(path.Dir(part)+"/_rels/"+path.Base(part)+".rels", part)
	if err != nil {
		return err
	}
	slide.each(drawingNS, "blip", func(picture *element) {
		id, embedded := picture.attr(officeRel, "embed")
		if !embedded {
			return
		}
		link, found := links[id]
		if !found || link.external || !strings.HasSuffix(link.kind, "/image") || archive.parts[link.target] == nil {
			out.find("slide %d references missing embedded media", number)
		}
	})
	return nil
}

// checkShape checks one shape's place and size against the slide.
func checkShape(shape *element, number, slideWidth, slideHeight int, out *structure) {
	var transform *element
	for _, space := range []string{drawingNS, slidesNS} {
		if transform == nil {
			shape.each(space, "xfrm", func(found *element) {
				if transform == nil {
					transform = found
				}
			})
		}
	}
	offset, extent := transform.child(drawingNS, "off"), transform.child(drawingNS, "ext")
	if offset == nil || extent == nil {
		// A shape placed by its layout has no geometry of its own.
		return
	}
	read := func(e *element, name string) (int, bool) {
		written, _ := e.attr("", name)
		value, err := strconv.Atoi(written)
		return value, err == nil
	}
	x, okX := read(offset, "x")
	y, okY := read(offset, "y")
	w, okW := read(extent, "cx")
	h, okH := read(extent, "cy")
	if !okX || !okY || !okW || !okH {
		out.find("slide %d has invalid shape geometry", number)
		return
	}
	// A connector is a line and may have no width or no height, not both.
	flat := w == 0 || h == 0
	if w < 0 || h < 0 || (w == 0 && h == 0) || (shape.name != "cxnSp" && flat) {
		out.find("slide %d has a shape with non-positive extent", number)
		return
	}
	// Only a shape wholly off the canvas blocks: partial bleed is a design
	// choice in many decks.
	if x > slideWidth || y > slideHeight || x+w < 0 || y+h < 0 {
		out.find("slide %d has a shape entirely off the canvas", number)
	}
}

// excelErrors are the values a formula's cached result holds when the
// formula failed.
var excelErrors = map[string]bool{"#REF!": true, "#DIV/0!": true, "#VALUE!": true, "#NAME?": true, "#NULL!": true, "#NUM!": true, "#N/A": true}

// checkXLSX checks a workbook: that it has sheets with something in them,
// and that every formula carries a cached result that is not an error. The
// cached result is what the page's viewer and any reader without Excel
// shows, so a formula written without one shows as an empty cell.
func checkXLSX(data []byte) structure {
	var out structure
	if len(data) == 0 {
		out.find("XLSX is empty")
		return out
	}
	const workbook, workbookRels = "xl/workbook.xml", "xl/_rels/workbook.xml.rels"
	archive, err := openOffice(data, "XLSX", "[Content_Types].xml", "_rels/.rels", workbook, workbookRels)
	if err != nil {
		out.find("%s", err)
		return out
	}
	invalid := func(err error) structure {
		var fault *errOffice
		if errors.As(err, &fault) {
			out.find("%s", fault)
		} else {
			out.find("XLSX is not valid OOXML")
		}
		return out
	}
	raw, err := archive.read(workbook, maxWorkbookXMLBytes)
	if err != nil {
		return invalid(err)
	}
	root, err := parseXML(raw)
	if err != nil {
		return invalid(err)
	}
	links, err := archive.relationships(workbookRels, workbook)
	if err != nil {
		return invalid(err)
	}
	var sheets []string
	root.each(sheetNS, "sheet", func(sheet *element) {
		id, _ := sheet.attr(officeRel, "id")
		if link, found := links[id]; found {
			sheets = append(sheets, link.target)
		}
	})
	if len(sheets) == 0 {
		out.find("XLSX workbook has no worksheets")
		return out
	}
	cells, filled := 0, 0
	for _, sheet := range sheets {
		if archive.parts[sheet] == nil {
			out.find("XLSX is missing worksheet part %s", sheet)
			continue
		}
		raw, err := archive.read(sheet, maxUncompressedBytes)
		if err != nil {
			return invalid(err)
		}
		tree, err := parseXML(raw)
		if err != nil {
			out.find("XLSX worksheet %s is not valid XML", sheet)
			continue
		}
		tooMany := false
		tree.each(sheetNS, "c", func(cell *element) {
			if cells++; cells > maxSheetCells {
				tooMany = true
				return
			}
			value := cell.child(sheetNS, "v")
			cached := ""
			if value != nil {
				cached = strings.TrimSpace(value.text)
			}
			switch {
			case cell.child(sheetNS, "f") != nil:
				if cached == "" {
					out.find("formula cell is missing a cached result value")
					return
				}
				if kind, _ := cell.attr("", "t"); kind == "e" || excelErrors[cached] {
					out.find("formula cell cached result is an Excel error (%s)", cached)
				}
				filled++
			case cached != "" || cell.child(sheetNS, "is") != nil:
				filled++
			}
		})
		if tooMany {
			return structure{findings: []string{fmt.Sprintf("XLSX has more than %d cells, which is more than can be checked; make it smaller", maxSheetCells)}}
		}
	}
	if filled == 0 && len(out.findings) == 0 {
		out.find("XLSX contains no non-empty cells")
	}
	out.findings = distinct(out.findings)
	return out
}

// The only hosts an interactive page may load from: the viewer's content
// security policy allows stylesheets and fonts from these and nothing else.
var fontHosts = map[string]bool{"fonts.googleapis.com": true, "fonts.gstatic.com": true}

// cssReference finds what a stylesheet loads: @import and url(...). The
// two are often written together, @import url("..."), which is one
// reference: read as an @import alone, its target would be the text "url(".
var cssReference = regexp.MustCompile(`(?i)(?:@import\s+(?:url\(\s*)?|url\(\s*)["']?([^"')\s;]+)`)

// loadable reports whether the viewer will load a reference.
func loadable(reference string) bool {
	reference = strings.TrimSpace(reference)
	if reference == "" || strings.HasPrefix(reference, "#") || strings.HasPrefix(reference, "data:") {
		return true
	}
	parsed, err := url.Parse(reference)
	return err == nil && parsed.Scheme == "https" && fontHosts[parsed.Hostname()]
}

// checkHTML checks an interactive page: that it is a UTF-8 fragment with
// markup in it and no document wrapper (the viewer supplies the document
// and its security policy), and notes each thing it tries to load that the
// viewer will block.
func checkHTML(data []byte) structure {
	var out structure
	if len(bytes.TrimSpace(data)) == 0 {
		out.find("HTML artifact is empty")
		return out
	}
	if !utf8.Valid(data) {
		out.find("HTML artifact must be UTF-8 encoded")
		return out
	}
	var (
		markup, wrapper, inStyle bool
		blocked                  []string
	)
	inspectCSS := func(css string) {
		for _, match := range cssReference.FindAllStringSubmatch(css, -1) {
			if !loadable(match[1]) {
				blocked = append(blocked, match[1])
			}
		}
	}
	tokens := html.NewTokenizer(bytes.NewReader(data))
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			if err := tokens.Err(); !errors.Is(err, io.EOF) {
				out.find("HTML artifact could not be parsed: %v", err)
				return out
			}
			break
		}
		switch kind {
		case html.DoctypeToken:
			markup, wrapper = true, true
		case html.StartTagToken, html.SelfClosingTagToken:
			markup = true
			token := tokens.Token()
			switch token.Data {
			case "html", "head", "body":
				wrapper = true
			}
			inStyle = token.Data == "style" && kind == html.StartTagToken
			for _, attribute := range token.Attr {
				switch attribute.Key {
				case "src", "href":
					if !loadable(attribute.Val) {
						blocked = append(blocked, strings.TrimSpace(attribute.Val))
					}
				case "style":
					inspectCSS(attribute.Val)
				}
			}
		case html.EndTagToken:
			name, _ := tokens.TagName()
			switch string(name) {
			case "html", "head", "body":
				wrapper = true
			case "style":
				inStyle = false
			}
		case html.TextToken:
			if inStyle {
				inspectCSS(string(tokens.Text()))
			}
		}
	}
	if !markup {
		out.find("HTML artifact contains no markup")
	}
	if wrapper {
		out.find("HTML artifact must be a fragment without doctype, html, head, or body tags")
	}
	for _, reference := range distinct(blocked) {
		out.notes = append(out.notes, "External resource will be blocked by the artifact viewer: "+reference)
	}
	return out
}

// checkPDFBytes is the part of a PDF's check that needs no PDF reader: that
// the bytes are a PDF at all. Its pages, text and fonts are measured in the
// sandbox by poppler, on a copy (verify.go).
func checkPDFBytes(data []byte) structure {
	var out structure
	switch {
	case len(data) == 0:
		out.find("PDF is empty")
	case !bytes.HasPrefix(data, []byte("%PDF-")):
		out.find("PDF could not be parsed: the file does not start with a PDF header")
	}
	return out
}

// distinct is the list without repeats, in its first order. A fault in a
// table's every cell is one thing to fix, not three hundred lines.
func distinct(items []string) []string {
	seen := make(map[string]bool, len(items))
	var out []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}
