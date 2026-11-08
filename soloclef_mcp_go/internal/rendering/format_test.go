package rendering_test

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/rendering"
)

// Counted in characters, not bytes: the Python original measures Python string
// length, so a byte count would cut a different place in any non-ASCII text.
func TestClipCountsCharactersNotBytes(t *testing.T) {
	text := strings.Repeat("é", 10)
	if got := rendering.Clip(text, 10); got != text {
		t.Errorf("a 10-character string was clipped at a limit of 10: %q", got)
	}
	got := rendering.Clip(text, 4)
	const want = "éééé\n\n… [6 more characters truncated]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClipLeavesShortTextAlone(t *testing.T) {
	if got := rendering.Clip("short", 100); got != "short" {
		t.Errorf("got %q", got)
	}
}

// A field that duplicates another verbatim — Reddit's html mirrors its body —
// only enlarges an inline result; the whole record stays in the stored run.
func TestCompactItemsDropsRedundantFieldsAndExcerptsLongOnes(t *testing.T) {
	source := `{"run_id":"r1","items":[{"url":"u","body":"` +
		strings.Repeat("x", 20) + `","html":"<p>same</p>","score":12}]}`
	value, err := pythonjson.DecodeAny([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	compacted := rendering.CompactItems(value, 5)
	rendered := pythonjson.Compact(compacted)
	if strings.Contains(rendered, "html") {
		t.Errorf("the redundant field survived: %s", rendered)
	}
	if !strings.Contains(rendered, "15 more characters truncated") {
		t.Errorf("the long field was not excerpted: %s", rendered)
	}
	for _, kept := range []string{`"run_id":"r1"`, `"url":"u"`, `"score":12`} {
		if !strings.Contains(rendered, kept) {
			t.Errorf("%s was lost: %s", kept, rendered)
		}
	}
}

// Only a reply shaped as an object with an items array is compacted; anything
// else passes through, as it does in the Python original.
func TestCompactItemsLeavesAnotherShapeAlone(t *testing.T) {
	for _, source := range []string{`{"other":1}`, `[1,2]`, `"text"`, `{"items":7}`} {
		value, err := pythonjson.DecodeAny([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if got := pythonjson.Compact(rendering.CompactItems(value, 5)); got != source {
			t.Errorf("%s became %s", source, got)
		}
	}
}

// Member order survives compaction, so the backend's own order reaches the model.
func TestCompactItemsKeepsMemberOrder(t *testing.T) {
	const source = `{"zebra":1,"items":[{"c":"x","a":"y","b":"z"}],"apple":2}`
	value, err := pythonjson.DecodeAny([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if got := pythonjson.Compact(rendering.CompactItems(value, 100)); got != source {
		t.Errorf("got %s, want %s", got, source)
	}
}
