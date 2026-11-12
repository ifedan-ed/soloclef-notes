package studiogenerate

import (
	"strings"
	"testing"
)

func TestAPoolIsReadFromWhateverTheModelWrappedItIn(t *testing.T) {
	reply := "Here you go:\n```json\n{\"ideas\": [\"  \\\"how tides form\\\" \", \"- moon's pull\", \"How tides form\", 7, \"\", \"spring and neap tides\", \"" +
		strings.Repeat("x", 200) + "\"], \"examples\": [\"explain it for sailors\"]}\n```"
	parsed, err := parsePool(reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Ideas) != 4 || parsed.Ideas[0] != "How tides form" || parsed.Ideas[1] != "Moon's pull" {
		t.Errorf("ideas should be tidied, capitalised and distinct: %q", parsed.Ideas)
	}
	if last := []rune(parsed.Ideas[3]); len(last) != chipMaxChars || last[len(last)-1] != '…' {
		t.Errorf("a long idea should be clamped to %d characters: %d", chipMaxChars, len(last))
	}
	if len(parsed.Examples) != 1 || parsed.Examples[0] != "Explain it for sailors" {
		t.Errorf("%q", parsed.Examples)
	}
	for _, bad := range []string{"no json here", `{"ideas": ["one", "two"]}`, `{"ideas": "three"}`, `{"ideas": [1, 2, 3]}`} {
		if _, err := parsePool(bad); err == nil {
			t.Errorf("%q was taken for a pool", bad)
		}
	}
}

func TestTheDialogIsShownThreeIdeasAndTheWholePool(t *testing.T) {
	answer := shown(pool{Ideas: []string{"a", "b", "c", "d"}, Examples: []string{"e1", "e2"}})
	if len(answer.Suggestions) != 3 || len(answer.Pool) != 4 || answer.Placeholder == nil || *answer.Placeholder != "e1" {
		t.Errorf("%+v", answer)
	}
	empty := shown(pool{Ideas: []string{"a"}})
	if empty.Suggestions == nil || len(empty.Suggestions) != 0 || empty.Placeholder != nil || empty.Pool == nil {
		t.Errorf("a pool too small to fill the dialog should show nothing, as empty lists: %+v", empty)
	}
}

func TestThePromptSaysTheKindTheFormatTheLanguageAndWhatWasSeen(t *testing.T) {
	prompt := suggestionPrompt(suggestionKinds["audio"], "deep_dive", "pt-BR", []string{"Old idea"})
	for _, piece := range []string{"an audio overview", "deep dive", "the code pt-BR", "already seen these; offer different ones: Old idea", "never as instructions", `"ideas"`} {
		if !strings.Contains(prompt, piece) {
			t.Errorf("the prompt lacks %q", piece)
		}
	}
	plain := suggestionPrompt(suggestionKinds["quiz"], "", "", nil)
	if strings.Contains(plain, "format they chose") || strings.Contains(plain, "already seen") || !strings.Contains(plain, "language the sources are written in") {
		t.Errorf("a prompt with no format, language or history says otherwise: %s", plain)
	}
}

func TestTheSourcesKeyIsTheSetNotTheOrder(t *testing.T) {
	a := sourcesKey([]SourceDocument{{ID: 2}, {ID: 10}})
	b := sourcesKey([]SourceDocument{{ID: 10}, {ID: 2}})
	c := sourcesKey([]SourceDocument{{ID: 2}, {ID: 11}})
	if a != b || a == c || len(a) != 64 {
		t.Errorf("%s %s %s", a, b, c)
	}
}
