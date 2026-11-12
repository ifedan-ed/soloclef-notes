// Package testpdf writes a small, real PDF with a text layer, for the tests
// that send a file to a live parser. Built in code so that no document, and
// certainly no person's document, is ever checked in.
package testpdf

import (
	"bytes"
	"fmt"
)

// Document is a PDF with one page per entry of pages. Each page shows its
// text as a single line of Helvetica. The text must be plain ASCII without
// parentheses or backslashes; this is a test fixture, not a PDF library.
func Document(pages ...string) []byte {
	var pdf bytes.Buffer
	var offsets []int
	object := func(body string) {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}

	pdf.WriteString("%PDF-1.4\n")
	// Objects: 1 catalog, 2 page tree, 3 font, then a page and its content
	// stream for each page.
	object("<< /Type /Catalog /Pages 2 0 R >>")
	kids := ""
	for index := range pages {
		kids += fmt.Sprintf("%d 0 R ", 4+2*index)
	}
	object(fmt.Sprintf("<< /Type /Pages /Kids [ %s] /Count %d >>", kids, len(pages)))
	object("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for index, text := range pages {
		object(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*index))
		stream := fmt.Sprintf("BT /F1 14 Tf 72 720 Td (%s) Tj ET", text)
		object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}

	// The cross-reference table: where each object starts, so a reader can
	// jump to it. Every entry is exactly 20 bytes.
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return pdf.Bytes()
}
