package notebooksearch

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func words(terms [][]rune) []string {
	out := make([]string, len(terms))
	for i, term := range terms {
		out[i] = string(term)
	}
	return out
}

func TestTheWordsOfAQueryWorthMatching(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"Asthma in the ADULT", []string{"asthma", "adult"}},
		{"nurse nurses nurse", []string{"nurses", "nurse"}},
		{"a b c", []string{}},
		{"snake_case, e-mail!", []string{"snake", "case", "mail"}},
		{"épinéphrine 0.3 mg", []string{"épinéphrine", "mg"}},
		{"one two three four five six seven eight nine ten", []string{"three", "seven", "eight", "four", "five", "one", "two", "six"}},
	}
	for _, c := range cases {
		if got := words(queryTerms(c.query)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("queryTerms(%q) = %q, want %q", c.query, got, c.want)
		}
	}
}

func TestMarkdownIsFlattenedToReadableText(t *testing.T) {
	cases := map[string]string{
		"# Title\n\nSome **bold** and `code`.":            "Title Some bold and code .",
		"See [the guide](https://example.test/a) now":     "See the guide now",
		"![chart](x.png) | a | b |":                       "chart a b",
		"> quoted\n<b>tag</b> ~~gone~~":                   "quoted tag gone",
		"a stray [ bracket (and paren) stays as written]": "a stray [ bracket (and paren) stays as written]",
		"": "",
	}
	for markdown, want := range cases {
		if got := plainText(markdown); got != want {
			t.Errorf("plainText(%q) = %q, want %q", markdown, got, want)
		}
	}
}

func TestATermMustStartAWord(t *testing.T) {
	text := []rune("Asthma, bronchial asthma; MA and Ma_x. Nurses nurse.")
	terms := queryTerms("ma asth nurse nurses")
	var got []string
	for _, m := range matches(text, terms) {
		got = append(got, string(text[m.start:m.end]))
	}
	if want := []string{"Asth", "asth", "MA", "Ma", "Nurses", "nurse"}; !reflect.DeepEqual(got, want) {
		t.Errorf("matched %q, want %q", got, want)
	}
}

// sliced is the snippet's highlighted pieces, cut the way the clients cut
// them: as a JavaScript string, by UTF-16 unit.
func sliced(shown string, highlights [][2]int) []string {
	units := utf16.Encode([]rune(shown))
	out := []string{}
	for _, h := range highlights {
		out = append(out, string(utf16.Decode(units[h[0]:h[1]])))
	}
	return out
}

func TestASnippetIsCutAroundItsMatchesAndHighlighted(t *testing.T) {
	terms := queryTerms("spacer inhaler")
	lead := strings.Repeat("filler words go here ", 30)
	shown, highlights := snippet(lead+"Use a spacer with the inhaler every time. "+lead, terms)
	if !strings.HasPrefix(shown, "… ") || !strings.HasSuffix(shown, " …") {
		t.Errorf("a snippet cut at both ends is not marked: %q", shown)
	}
	if n := len([]rune(shown)); n > snippetChars+4 {
		t.Errorf("the snippet is %d characters", n)
	}
	if got := sliced(shown, highlights); !reflect.DeepEqual(got, []string{"spacer", "inhaler"}) {
		t.Errorf("highlighted %q in %q", got, shown)
	}

	// No match: the start of the passage, uncut and unmarked when short.
	shown, highlights = snippet("A short passage about something else.", terms)
	if shown != "A short passage about something else." || len(highlights) != 0 || highlights == nil {
		t.Errorf("an unmatched passage: %q %v", shown, highlights)
	}

	// The clients index by UTF-16 unit: an emoji is two.
	shown, highlights = snippet("😀😀 the spacer", terms)
	if !reflect.DeepEqual(highlights, [][2]int{{9, 15}}) || !reflect.DeepEqual(sliced(shown, highlights), []string{"spacer"}) {
		t.Errorf("after two emoji the highlight is %v", highlights)
	}

	if shown, highlights = snippet("", terms); shown != "" || len(highlights) != 0 {
		t.Errorf("an empty passage: %q %v", shown, highlights)
	}
}

func TestTheWindowWithTheMostDifferentTermsWins(t *testing.T) {
	terms := queryTerms("alpha beta")
	gap := strings.Repeat("x ", 200)
	text := "alpha alpha alpha " + gap + "alpha beta " + gap
	shown, _ := snippet(text, terms)
	if !strings.Contains(shown, "alpha beta") {
		t.Errorf("the snippet shows the repeated term, not both: %q", shown)
	}
}

// A passage of one word repeated must not take time in the square of its
// length: this is 200,000 matches.
func TestALongPassageOfMatchesIsLinear(t *testing.T) {
	text := strings.Repeat("alpha ", 200_000)
	if shown, highlights := snippet(text, queryTerms("alpha")); shown == "" || len(highlights) == 0 {
		t.Error("nothing was shown")
	}
}
