package objecthttp

import (
	"math"
	"strconv"
	"strings"
)

// RangeKind is what a Range header asks of an object of a known size.
type RangeKind int

const (
	// Whole: send the entire object with a 200. Also the answer to a Range
	// header this server does not act on (see ParseRange).
	Whole RangeKind = iota
	// Partial: send Offset..Offset+Length-1 with a 206.
	Partial
	// Unsatisfiable: a well-formed range that lies wholly outside the
	// object; the answer is a 416 naming the object's size.
	Unsatisfiable
)

// ByteRange is the part of an object a request asked for.
type ByteRange struct {
	Kind   RangeKind
	Offset int64
	Length int64
}

// ParseRange reads a Range header against an object's size.
//
// The three forms of one range are all read: `bytes=500-999`, `bytes=500-`
// (from 500 to the end) and `bytes=-500` (the last 500 bytes). An end past
// the object is clamped to its last byte, as RFC 9110 section 14.1.2 says.
//
// Two kinds of header are deliberately answered with the whole object and a
// 200 rather than refused, and both differ from the Python route, which
// answered 416 to each:
//
//   - A header that is not a valid byte range at all (another unit, letters
//     where digits belong, an end before the start). RFC 9110 section 14.2
//     says a server ignores a Range it cannot parse. A 416 there tells a
//     client the *object* cannot satisfy it, which is false, and a player
//     that gets a 416 stops.
//   - Several ranges at once (`bytes=0-9,20-29`). Honouring them means a
//     multipart/byteranges body, which no client of these routes asks for --
//     a browser's media element and a PDF viewer both send one range at a
//     time. Ignoring the header is always permitted, and the client gets
//     every byte it wanted.
//
// 416 is kept for the one case it describes: a well-formed range that starts
// at or past the end of the object, or a suffix of zero bytes.
func ParseRange(header string, size int64) ByteRange {
	whole := ByteRange{Kind: Whole, Offset: 0, Length: size}
	// The unit is case-insensitive and "bytes" is the only one there is.
	const unit = "bytes="
	if len(header) < len(unit) || !strings.EqualFold(header[:len(unit)], unit) {
		return whole
	}
	spec := strings.TrimSpace(header[len(unit):])
	if strings.Contains(spec, ",") {
		return whole
	}
	first, last, dashed := strings.Cut(spec, "-")
	if !dashed {
		return whole
	}
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)

	if first == "" {
		// The suffix form: the last N bytes.
		suffix, ok := digits(last)
		if !ok {
			return whole
		}
		// An empty object has no last bytes, and a suffix of nothing is no
		// bytes of anything: neither can be satisfied.
		if suffix == 0 || size == 0 {
			return ByteRange{Kind: Unsatisfiable}
		}
		if suffix > size {
			suffix = size
		}
		return ByteRange{Kind: Partial, Offset: size - suffix, Length: suffix}
	}

	start, ok := digits(first)
	if !ok {
		return whole
	}
	end := size - 1
	if last != "" {
		stated, ok := digits(last)
		if !ok || stated < start {
			return whole
		}
		if stated < end {
			end = stated
		}
	}
	if start >= size {
		return ByteRange{Kind: Unsatisfiable}
	}
	return ByteRange{Kind: Partial, Offset: start, Length: end - start + 1}
}

// digits reads a run of ASCII digits and nothing else: no sign, no spaces,
// no underscores. strconv alone would accept "+5", which is not a byte
// position. A run too long for an int64 is read as the largest one, because
// it is still a well-formed position -- merely one past the end of anything
// this store could hold -- and must be judged as such, not as a typo.
func digits(text string) (int64, bool) {
	if text == "" {
		return 0, false
	}
	for _, character := range []byte(text) {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return math.MaxInt64, true
	}
	return value, true
}
