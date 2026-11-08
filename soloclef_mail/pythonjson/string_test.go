package pythonjson

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestEveryFloatRendersAsPythonsRepr(t *testing.T) {
	raw, err := os.ReadFile("testdata/python_float_repr.json")
	if err != nil {
		t.Fatal(err)
	}
	var reprs []string
	if err := json.Unmarshal(raw, &reprs); err != nil {
		t.Fatal(err)
	}
	if len(reprs) < 100 {
		t.Fatalf("expected a sweep, got %d values", len(reprs))
	}
	for _, text := range reprs {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			t.Fatalf("Python wrote %q, which Go cannot parse: %v", text, err)
		}
		got, err := FormatFloat(value)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if got != text {
			t.Errorf("Python writes %s, this writes %s", text, got)
		}
	}
}

func TestANotANumberIsRefusedRatherThanInvented(t *testing.T) {
	// json.dumps runs with allow_nan at its default, so Python would have
	// written Infinity -- which no JSON reader accepts. Refusing is the only
	// answer that cannot put an unreadable entry on the queue.
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := FormatFloat(f); err == nil {
			t.Errorf("%v was accepted", f)
		}
	}
}

func TestEnsureAsciiAndNotAreTheTwoSpellingsPythonWrites(t *testing.T) {
	// Both are json.dumps; the flag is the only difference, and the one that
	// matters is the HTML in an email body: Go's own encoder would escape the
	// angle brackets and the ampersand in either mode.
	cases := []struct {
		text  string
		ascii string
		utf8  string
	}{
		{"<p>a &amp; b</p>", `"<p>a &amp; b</p>"`, `"<p>a &amp; b</p>"`},
		{"Gr\u00fc\u00dfe \u2014", `"Gr\u00fc\u00dfe \u2014"`, "\"Gr\u00fc\u00dfe \u2014\""},
		{"\U0001f600", `"\ud83d\ude00"`, "\"\U0001f600\""},
		{"\u2028line", `"\u2028line"`, "\"\u2028line\""},
		// DEL is escaped by ensure_ascii and written as itself without it,
		// because that flag's raw range is 0x20..0x7e. The tab keeps its short
		// escape either way, and the other C0 control gets \u either way.
		{"tab\there \x01 \x7f", `"tab\there \u0001 \u007f"`, "\"tab\\there \\u0001 \u007f\""},
	}
	for _, c := range cases {
		var ascii, utf8 strings.Builder
		WriteASCIIOnly(&ascii, c.text)
		WriteUTF8(&utf8, c.text)
		if ascii.String() != c.ascii {
			t.Errorf("ensure_ascii=True wrote %s, Python writes %s", ascii.String(), c.ascii)
		}
		if utf8.String() != c.utf8 {
			t.Errorf("ensure_ascii=False wrote %s, Python writes %s", utf8.String(), c.utf8)
		}
	}
}
