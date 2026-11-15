package studiogenerate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A title past the bound is cut, on a whole word where the script has
// words, and never through a character.
func TestATitleIsCutToItsBoundOnAWholeWord(t *testing.T) {
	if got := cutTitle("  Quiz:  Tides\n"); got != "Quiz: Tides" {
		t.Errorf("a short title: %q", got)
	}
	words := strings.Repeat("moon ", 60) // 300 characters
	cut := cutTitle(words)
	if n := utf8.RuneCountInString(cut); n > maxTitleChars || n < maxTitleChars-5 || !strings.HasSuffix(cut, "moon") {
		t.Errorf("a long title of words was cut to %d characters: …%q", n, cut[len(cut)-10:])
	}
	// The cut would fall inside "pull": it backs off to the word before.
	inside := strings.Repeat("a", 197) + " pull the sea"
	if got := cutTitle(inside); got != strings.Repeat("a", 197) {
		t.Errorf("a cut inside a word: …%q", got[len(got)-5:])
	}
	// Chinese has no spaces: cut at the bound, each character whole.
	chinese := strings.Repeat("潮汐与月球引力", 40) // 280 characters, 3 bytes each
	got := cutTitle(chinese)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != maxTitleChars || !strings.HasPrefix(chinese, got) {
		t.Errorf("a Chinese title was cut to %d characters, valid %v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
	// One word longer than half the bound is cut at the bound, not
	// dropped whole.
	long := "Infographic: " + strings.Repeat("é", 250)
	if got := cutTitle(long); utf8.RuneCountInString(got) != maxTitleChars || !utf8.ValidString(got) {
		t.Errorf("one long word: %d characters", utf8.RuneCountInString(got))
	}
}

// A dialog's title is never refused for its length: an installed phone
// sends "<label>: <notebook>" whatever the notebook is called.
func TestALongDialogTitleIsCutNotRefused(t *testing.T) {
	body := `{"kind":"infographic","title":"Infographic: ` + strings.Repeat("Moon ", 80) + `"}`
	r, failures := parseRequest([]byte(body))
	if len(failures) > 0 {
		t.Fatalf("a long title was refused: %+v", failures)
	}
	if n := utf8.RuneCountInString(r.Title); n > maxTitleChars || !strings.HasPrefix(r.Title, "Infographic: Moon") {
		t.Errorf("the title was kept as %d characters: %q", n, r.Title)
	}
	if got := outputTitle(request(t, `{"kind":"report"}`), strings.Repeat("tide ", 60)); utf8.RuneCountInString(got) > maxTitleChars || !strings.HasSuffix(got, "tide") {
		t.Errorf("the kind's default for a long notebook: %q", got)
	}
}
