package pythonjson

import (
	"strings"
	"testing"
)

func TestWriteUTF8IsTheSpellingPythonWritesWithoutEnsureASCII(t *testing.T) {
	// The case that matters is the HTML in an email body: Go's own encoder
	// would escape the angle brackets and the ampersand, and U+2028.
	cases := []struct {
		text string
		want string
	}{
		{"<p>a &amp; b</p>", `"<p>a &amp; b</p>"`},
		{"Grüße —", "\"Grüße —\""},
		{"\U0001f600", "\"\U0001f600\""},
		{" line", "\" line\""},
		// The tab keeps its short escape, another C0 control gets \u, and DEL
		// is written as itself.
		{"tab\there \x01 \x7f", "\"tab\\there \\u0001 \u007f\""},
	}
	for _, c := range cases {
		var got strings.Builder
		WriteUTF8(&got, c.text)
		if got.String() != c.want {
			t.Errorf("WriteUTF8(%q) wrote %s, Python writes %s", c.text, got.String(), c.want)
		}
	}
}
