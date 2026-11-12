// Package picturestest makes, for the tests of other modules, a real PDF
// with a photo drawn on one page: the fixture soloclef_ingest's own tests
// use (internal/testpdf), which a module outside this one cannot import.
package picturestest

import (
	"bytes"
	"image"
	"image/jpeg"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/internal/testpdf"
)

// Photo is a JPEG of seeded noise, side pixels square: it does not
// compress under the kilobyte that marks decoration, and two seeds make two
// different pictures.
func Photo(side int, seed uint32) []byte {
	picture := image.NewRGBA(image.Rect(0, 0, side, side))
	for index := range picture.Pix {
		seed = seed*1664525 + 1013904223
		picture.Pix[index] = byte(seed >> 24)
	}
	var out bytes.Buffer
	// Encoding into memory fails only on a nil picture.
	_ = jpeg.Encode(&out, picture, &jpeg.Options{Quality: 90})
	return out.Bytes()
}

// PDF is a PDF of one line of text a page, with a 64-pixel photo drawn on
// page photoPage (counted from 1).
func PDF(pages []string, photoPage int) []byte {
	return testpdf.WithPictures(pages, testpdf.Picture{Page: photoPage, JPEG: Photo(64, 11), Width: 64, Height: 64})
}
