package testpdf

import (
	"bytes"
	"strconv"
	"testing"
)

func TestTheCrossReferenceTablePointsAtEachObject(t *testing.T) {
	pdf := Document("First page", "Second page")

	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatal("not framed as a PDF")
	}
	// startxref names the table's offset.
	marker := bytes.LastIndex(pdf, []byte("startxref\n"))
	tail := pdf[marker+len("startxref\n"):]
	xref, err := strconv.Atoi(string(tail[:bytes.IndexByte(tail, '\n')]))
	if err != nil || !bytes.HasPrefix(pdf[xref:], []byte("xref\n0 8\n")) {
		t.Fatalf("startxref = %d (%v), which is not the table", xref, err)
	}
	// Each entry names the offset of "<n> 0 obj". Two pages: seven objects.
	entries := pdf[xref+len("xref\n0 8\n")+20:]
	for number := 1; number <= 7; number++ {
		offset, err := strconv.Atoi(string(entries[(number-1)*20 : (number-1)*20+10]))
		want := []byte(strconv.Itoa(number) + " 0 obj\n")
		if err != nil || !bytes.HasPrefix(pdf[offset:], want) {
			t.Errorf("object %d: offset %d does not start %q", number, offset, want)
		}
	}
}
