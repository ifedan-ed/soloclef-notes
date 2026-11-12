package vision

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"

	// Decoders registered with image.Decode for the formats a PDF or a
	// camera hands over that a vision model does not take as they are.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	"golang.org/x/image/webp"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// The bounds on a picture sent to a vision model. They are the providers'
// own, not a deployment's, so they are constants.
const (
	// MaxSentBytes is the most a picture may be once encoded: Anthropic's
	// per-image limit, the strictest of the providers the proxy reaches.
	MaxSentBytes = 5 << 20
	// MaxConvertibleBytes is the most a picture may be before it is
	// decoded to be made smaller. A raw TIFF or BMP is uncompressed, so it
	// can be large and still shrink to well under MaxSentBytes.
	MaxConvertibleBytes = 40 << 20
	// MaxLongEdge is the longest side a re-encoded picture keeps: the
	// providers scale anything longer down to about this anyway, so the
	// pixels past it cost upload time and buy nothing.
	MaxLongEdge = 2048
	// MaxPixels refuses a decompression bomb before it is decoded: the
	// header says the size, and a picture of this many pixels is already
	// 400 MB as RGBA.
	MaxPixels = 100_000_000
)

// Format is a picture's encoding, read from its first bytes. A name or a
// Content-Type can lie (a PDF names its JPEG 2000 images ".png"), the bytes
// cannot.
type Format string

const (
	PNG     Format = "image/png"
	JPEG    Format = "image/jpeg"
	GIF     Format = "image/gif"
	WebP    Format = "image/webp"
	TIFF    Format = "image/tiff"
	BMP     Format = "image/bmp"
	HEIC    Format = "image/heic"
	JPEG2K  Format = "image/jp2"
	Unknown Format = ""
)

// Sniff is the format of an encoded picture.
func Sniff(data []byte) Format {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return PNG
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return JPEG
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return GIF
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return WebP
	case bytes.HasPrefix(data, []byte("II*\x00")), bytes.HasPrefix(data, []byte("MM\x00*")):
		return TIFF
	case bytes.HasPrefix(data, []byte("BM")) && len(data) > 26:
		return BMP
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) && heicBrands[string(data[8:12])]:
		return HEIC
	case bytes.HasPrefix(data, []byte("\x00\x00\x00\x0cjP  \r\n\x87\n")), bytes.HasPrefix(data, []byte{0xFF, 0x4F, 0xFF, 0x51}):
		return JPEG2K
	}
	return Unknown
}

// heicBrands are the ISO media brands a HEIC or HEIF picture declares.
var heicBrands = map[string]bool{"heic": true, "heix": true, "hevc": true, "hevx": true, "heim": true, "heis": true, "mif1": true, "msf1": true}

// web are the formats every provider takes as they are.
var web = map[Format]bool{PNG: true, JPEG: true, GIF: true, WebP: true}

// refused is a picture that cannot be sent, for a reason that will not pass.
func refused(kind parse.Kind, format string, arguments ...any) error {
	return &parse.Error{Kind: kind, Parser: parse.Vision, Message: fmt.Sprintf(format, arguments...)}
}

// Prepare is a picture as it is sent: its media type and its bytes.
//
// A picture in a format every provider takes, and small enough, is sent as
// it is: re-encoding it would cost time and detail for nothing. Anything
// else that can be decoded (a TIFF scan, a BMP, a web picture over the
// limit) is scaled to MaxLongEdge and sent as a JPEG. HEIC is sent as it is
// when it fits, since no decoder for it is linked here and the providers
// that read it read it directly; one that does not says so, and that is
// the error the caller sees.
//
// Better than the Python: a JPEG over five megabytes, a phone's photo,
// was skipped there; here it is made smaller and described.
func Prepare(data []byte) (Format, []byte, error) {
	format := Sniff(data)
	switch {
	case format == Unknown:
		return "", nil, refused(parse.UnsupportedFormat, "the file is not a picture in a format this server can read (PNG, JPEG, GIF, WebP, TIFF, BMP or HEIC)")
	case format == JPEG2K:
		return "", nil, refused(parse.UnsupportedFormat, "the picture is JPEG 2000, which no vision model takes and this server cannot convert")
	case web[format] && len(data) <= MaxSentBytes:
		return format, data, nil
	case format == HEIC:
		if len(data) > MaxSentBytes {
			return "", nil, refused(parse.TooLarge, "the HEIC picture is %.1f MB, over the %d MB a vision model takes, and this server cannot make it smaller", float64(len(data))/(1<<20), MaxSentBytes>>20)
		}
		return format, data, nil
	case len(data) > MaxConvertibleBytes:
		return "", nil, refused(parse.TooLarge, "the picture is %.1f MB, over the %d MB this server will decode", float64(len(data))/(1<<20), MaxConvertibleBytes>>20)
	}
	picture, err := decode(data, format)
	if err != nil {
		return "", nil, err
	}
	encoded, err := shrink(picture)
	if err != nil {
		return "", nil, err
	}
	return JPEG, encoded, nil
}

// decode reads a picture after checking, from its header alone, that it is
// not too many pixels to hold.
func decode(data []byte, format Format) (image.Image, error) {
	var config image.Config
	var err error
	if format == WebP {
		config, err = webp.DecodeConfig(bytes.NewReader(data))
	} else {
		config, _, err = image.DecodeConfig(bytes.NewReader(data))
	}
	if err != nil {
		return nil, refused(parse.ParseFailed, "the picture's header cannot be read: %v", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > MaxPixels {
		return nil, refused(parse.TooLarge, "the picture is %d by %d pixels, more than this server will decode", config.Width, config.Height)
	}
	var picture image.Image
	switch format {
	case WebP:
		picture, err = webp.Decode(bytes.NewReader(data))
	case GIF:
		// The first frame: what a reader of the document sees first.
		picture, err = gif.Decode(bytes.NewReader(data))
	case PNG:
		picture, err = png.Decode(bytes.NewReader(data))
	case JPEG:
		picture, err = jpeg.Decode(bytes.NewReader(data))
	default:
		picture, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, refused(parse.ParseFailed, "the picture cannot be decoded: %v", err)
	}
	return picture, nil
}

// shrink scales a picture to MaxLongEdge and encodes it as a JPEG under
// MaxSentBytes. Transparency is laid on white, which is what a page under
// the picture would show.
func shrink(picture image.Image) ([]byte, error) {
	bounds := picture.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if long := max(width, height); long > MaxLongEdge {
		width, height = max(1, width*MaxLongEdge/long), max(1, height*MaxLongEdge/long)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	// CatmullRom keeps the edges of text and line drawings sharp, which is
	// most of what a picture in a document is.
	xdraw.CatmullRom.Scale(canvas, canvas.Bounds(), picture, bounds, xdraw.Over, nil)
	// A 2048-pixel JPEG is almost always under a megabyte at 85; the lower
	// qualities are for a picture of pure noise.
	for _, quality := range []int{85, 70, 50} {
		var out bytes.Buffer
		if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: quality}); err != nil {
			return nil, refused(parse.ParseFailed, "the picture could not be re-encoded: %v", err)
		}
		if out.Len() <= MaxSentBytes {
			return out.Bytes(), nil
		}
	}
	return nil, refused(parse.TooLarge, "the picture stays over %d MB even made smaller", MaxSentBytes>>20)
}
