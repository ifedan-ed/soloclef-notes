package principal

import (
	"bytes"
	"testing"
)

// Every expectation here was read out of PyJWT: jwt.utils.base64url_decode was
// run on each input in the Python tier's own image and its result, or the fact
// that it raised, recorded. None of them was derived from the Go code.
func TestSegmentsAreDecodedAsPyJWTDecodesThem(t *testing.T) {
	refused := "\x00refused"
	cases := map[string]string{
		"QUJDRA":     "ABCD",
		"QUJDRA==":   "ABCD",
		"QUJDRA=":    "ABCD",
		"QUJDRA====": "ABCD",
		"QUJD":       "ABC",
		"QUJ":        "AB",
		"QU":         "A",
		"Q":          refused,
		"QUJD!!!":    "ABC",
		"QUJDRA!!":   refused,
		"QUJ D":      "ABC",
		"QUJD ":      "ABC",
		" QUJD":      "ABC",
		"QUJD.QUJD":  "ABCABC",
		"!!!!":       "",
		"":           "",
		// A real segment is 2 past a multiple of four, so whether a stray space
		// is read at all depends on how many there are.
		" QUJDRA":  refused,
		"QUJDRA ":  refused,
		"QUJDRA  ": refused,
		// Three spaces pad the group out again.
		"QUJDRA   ": "ABCD",
		" QUJ":      refused,
		"QUJ ":      refused,
		// A padding run ends the input: what follows it is not read.
		"QQ==QQ==": "A",
		"QQ=Q":     refused,
	}
	for segment, want := range cases {
		decoded, err := decodePythonBase64URL(segment)
		if want == refused {
			if err == nil {
				t.Errorf("%q: read as %q, want refused", segment, decoded)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", segment, err)
			continue
		}
		if !bytes.Equal(decoded, []byte(want)) {
			t.Errorf("%q: got %q, want %q", segment, decoded, want)
		}
	}
}
