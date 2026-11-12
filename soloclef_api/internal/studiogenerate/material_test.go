package studiogenerate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEverySourceKeepsAFairShareOfTheBudget(t *testing.T) {
	long := "# Intro\n\n" + strings.Repeat("alpha ", 4000) + "\n\n# Results\n\n" + strings.Repeat("omega ", 4000)
	documents := []SourceDocument{{1, "Long  paper", long}, {2, "Note", "A short note."}}
	material := compileMaterial(documents, 2000)
	if n := utf8.RuneCountInString(material); n > 2000 {
		t.Errorf("the material is %d characters, over its budget of 2000", n)
	}
	for _, piece := range []string{"## Source: Long paper", "## Source: Note", "A short note.", "# Intro", "# Results", "omega"} {
		if !strings.Contains(material, piece) {
			t.Errorf("the material lacks %q: a long source must keep its later sections and a short one its whole text", piece)
		}
	}
	if compileMaterial(nil, 100) != "" {
		t.Error("no sources should be no material")
	}
}

func TestATextIsFittedBySectionAndNeverPastItsBudget(t *testing.T) {
	if fit("  short  ", 100) != "short" {
		t.Error("a text that fits should be itself, trimmed")
	}
	unstructured := strings.Repeat(strings.Repeat("word ", 50)+"\n\n", 40) + "the very end"
	for _, budget := range []int{0, 1, 50, 500, 5000} {
		got := fit(unstructured, budget)
		if n := utf8.RuneCountInString(got); n > budget {
			t.Errorf("budget %d: got %d characters", budget, n)
		}
	}
	// A text with no headings still keeps something from near its end.
	if got := fit(unstructured, 5000); !strings.Contains(got, "the very end") && !strings.Contains(got[len(got)/2:], "word") {
		t.Errorf("the fitted text keeps nothing from its later half")
	}
	if whole := wholeMaterial([]SourceDocument{{1, "A", "one"}, {2, "B", "two"}}); whole != "## Source: A\n\none\n\n## Source: B\n\ntwo" {
		t.Errorf("%q", whole)
	}
}
