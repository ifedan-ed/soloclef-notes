package testpdf

import (
	"bytes"
	"fmt"
)

// Picture is a JPEG to embed, and the page (counted from 1) it is drawn on.
type Picture struct {
	Page          int
	JPEG          []byte
	Width, Height int
}

// WithPictures is Document with JPEG pictures drawn on their pages, each
// below the page's line of text: a real image XObject (DCTDecode), the way
// a scanner or a word processor embeds a photo, so a test of image
// extraction reads what a person's PDF holds and not a stand-in.
func WithPictures(pages []string, pictures ...Picture) []byte {
	var pdf bytes.Buffer
	var offsets []int
	begin := func() int {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n", len(offsets))
		return len(offsets)
	}
	object := func(body string) int {
		number := begin()
		fmt.Fprintf(&pdf, "%s\nendobj\n", body)
		return number
	}

	pdf.WriteString("%PDF-1.4\n")
	// 1 catalog, 2 page tree, 3 font, then the pictures, then each page
	// and its content stream.
	object("<< /Type /Catalog /Pages 2 0 R >>")
	pageObject := func(index int) int { return 4 + len(pictures) + 2*index }
	kids := ""
	for index := range pages {
		kids += fmt.Sprintf("%d 0 R ", pageObject(index))
	}
	object(fmt.Sprintf("<< /Type /Pages /Kids [ %s] /Count %d >>", kids, len(pages)))
	object("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for _, picture := range pictures {
		begin()
		fmt.Fprintf(&pdf, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB "+
			"/BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", picture.Width, picture.Height, len(picture.JPEG))
		pdf.Write(picture.JPEG)
		pdf.WriteString("\nendstream\nendobj\n")
	}
	for index, text := range pages {
		xobjects, draw := "", ""
		for number, picture := range pictures {
			if picture.Page == index+1 {
				name := fmt.Sprintf("Im%d", number+1)
				xobjects += fmt.Sprintf("/%s %d 0 R ", name, 4+number)
				draw += fmt.Sprintf(" q 200 0 0 200 72 400 cm /%s Do Q", name)
			}
		}
		resources := "/Font << /F1 3 0 R >>"
		if xobjects != "" {
			resources += " /XObject << " + xobjects + ">>"
		}
		object(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << %s >> /Contents %d 0 R >>",
			resources, pageObject(index)+1))
		stream := fmt.Sprintf("BT /F1 14 Tf 72 720 Td (%s) Tj ET%s", text, draw)
		object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}

	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return pdf.Bytes()
}
