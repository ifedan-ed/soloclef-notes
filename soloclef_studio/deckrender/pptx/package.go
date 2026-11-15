// Package pptx writes a .pptx file: blank 16:9 slides with a background,
// preset shapes, text boxes, straight lines with or without an arrowhead,
// speaker notes and a transition. It is what the deck renderer needs and
// nothing more.
//
// Origin: PedsHub Tools' Go writer (tools-go internal/ooxml and
// internal/ooxml/pptx, same owner), which writes what python-pptx 1.0.2
// writes, part for part, and embeds python-pptx's default template. Tools
// had to match python-pptx byte for byte because it replaced a Python
// renderer and proved itself against it; Soloclef replaces nothing that
// way, so the Python quirks are gone: a value out of range is clamped by
// the caller, not raised as a Python ValueError; there are no charts,
// tables or pictures; shape names say what each shape is ("Bullet 2"), so
// a layout finding can be read; the template carries no printer settings,
// no thumbnail and no python-pptx author; and the shapes refer to no theme
// effect, so LibreOffice draws no shadow (Tools' "shadow fix", applied to
// lines as well).
package pptx

import (
	"archive/zip"
	"compress/flate"
	"fmt"
	"io"
	"sort"
	"strings"
)

// xmlHead is the declaration every XML part starts with.
const xmlHead = "<?xml version='1.0' encoding='UTF-8' standalone='yes'?>\n"

// EmuPerPt is DrawingML's unit: 12700 English Metric Units to the point.
const EmuPerPt = 12700

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#13;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;", "\r", "&#13;", "\t", "&#9;")
)

func escapeText(s string) string { return textEscaper.Replace(xmlSafe(s)) }
func escapeAttr(s string) string { return attrEscaper.Replace(xmlSafe(s)) }

// xmlSafe drops what XML 1.0 cannot hold. The content is checked before it
// is drawn (deckspec.Check refuses control characters), so this only
// stands between a missed character and a file PowerPoint will not open.
func xmlSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
		case r >= 0x20 && r <= 0xD7FF, r >= 0xE000 && r <= 0xFFFD, r >= 0x10000 && r <= 0x10FFFF:
		default:
			return -1
		}
		return r
	}, s)
}

// rel is one relationship of a part.
type rel struct{ id, typ, target string }

const relBase = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"

func relsXML(rels []rel) []byte {
	var b strings.Builder
	b.WriteString(xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range rels {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"/>`, r.id, r.typ, escapeAttr(r.target))
	}
	b.WriteString("</Relationships>")
	return []byte(b.String())
}

// part is one file of the package.
type part struct {
	name        string // without the leading slash
	contentType string // "" for a .rels part
	data        []byte
}

// contentTypes is [Content_Types].xml: rels and xml as Defaults, every
// other part an Override, sorted so the file is the same for the same deck.
func contentTypes(parts []part) []byte {
	overrides := map[string]string{}
	for _, p := range parts {
		if !strings.HasSuffix(p.name, ".rels") {
			overrides["/"+p.name] = p.contentType
		}
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(xmlHead + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`)
	for _, name := range names {
		fmt.Fprintf(&b, `<Override PartName="%s" ContentType="%s"/>`, escapeAttr(name), overrides[name])
	}
	b.WriteString("</Types>")
	return []byte(b.String())
}

// writePackage writes the zip: [Content_Types].xml first, then the parts in
// order, every entry deflated by one compressor reset between entries
// rather than a new one per entry (Tools measured that as most of a small
// deck's write time).
func writePackage(w io.Writer, parts []part) error {
	z := zip.NewWriter(w)
	var compressor *flate.Writer
	z.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		if compressor == nil {
			var err error
			if compressor, err = flate.NewWriter(out, flate.DefaultCompression); err != nil {
				return nil, err
			}
		} else {
			compressor.Reset(out)
		}
		return compressor, nil
	})
	all := append([]part{{name: "[Content_Types].xml", data: contentTypes(parts)}}, parts...)
	for _, p := range all {
		f, err := z.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate})
		if err != nil {
			return err
		}
		if _, err := f.Write(p.data); err != nil {
			return err
		}
	}
	return z.Close()
}
