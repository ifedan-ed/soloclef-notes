package studioviews

import (
	"strings"
	"testing"
)

// The report the web's own outline test splits
// (soloclef_web/tests/unit/notebooks/report-view.test.ts): the two must
// agree on which section is which, or a card lands under the wrong one.
const sampleReport = "# CHAM PICU Resident Orientation\n\n*Prepared for new residents*\n\nWelcome to the unit.\n\n" +
	"## 1. Introduction\n\nThe PICU cares for critically ill children.\n\n" +
	"## 2. Navigating the PICU: Rounds\n\nDay rounds start at 08:00; night handover at 19:00.\n\n" +
	"### Day shift\n\nPre-round by 07:30.\n\n```python\n## not a heading inside code\n```\n\n" +
	"## 3. Communication & Escalation\n\nCall the fellow first, then the attending.\n\n" +
	"## Summary\n\nBe early and ask questions.\n"

func TestAReportIsSplitWhereItsContentsListSplitsIt(t *testing.T) {
	out := splitReport(sampleReport)
	if out.title != "CHAM PICU Resident Orientation" {
		t.Errorf("title %q", out.title)
	}
	var headings []string
	for i, s := range out.sections {
		if s.index != i {
			t.Errorf("section %d has index %d", i, s.index)
		}
		headings = append(headings, s.heading)
	}
	if got := strings.Join(headings, "|"); got != "Introduction|Navigating the PICU: Rounds|Communication & Escalation|Summary" {
		t.Errorf("sections %q", got)
	}
	if rounds := out.sections[1].text; !strings.Contains(rounds, "### Day shift") || !strings.Contains(rounds, "## not a heading inside code") {
		t.Errorf("a section must keep its sub-heading and its fenced code: %q", rounds)
	}
	if body := out.sections[0].body(); body != "The PICU cares for critically ill children." {
		t.Errorf("body %q", body)
	}

	for name, c := range map[string]struct {
		markdown string
		title    string
		sections []string
	}{
		"no headings":                    {"Just prose.\n\nMore.", "", nil},
		"a title and nothing else":       {"# Only a title\n\nText.", "Only a title", nil},
		"text before the first heading":  {"Intro.\n\n# A\n\nx\n\n# B\n\ny", "", []string{"A", "B"}},
		"no level repeats: shallowest":   {"# T\n\n## One\n\n### Deep\n", "T", []string{"One"}},
		"the repeating level, not above": {"# T\n\n## Part\n\n### A\n\n### B\n", "T", []string{"A", "B"}},
		"windows line ends, emphasis":    {"# T\r\n\r\n## **1) Bold**\r\n\r\nx\r\n\r\n## IV. `Code`\r\n", "T", []string{"Bold", "Code"}},
		"a bare number is not numbering": {"## 2024 in review\n\n## 2025 ahead\n", "", []string{"2024 in review", "2025 ahead"}},
		"a tilde fence":                  {"## A\n\n~~~\n## no\n~~~\n\n## B\n", "", []string{"A", "B"}},
	} {
		got := splitReport(c.markdown)
		var listed []string
		for _, s := range got.sections {
			listed = append(listed, s.heading)
		}
		if got.title != c.title || strings.Join(listed, "|") != strings.Join(c.sections, "|") {
			t.Errorf("%s: title %q sections %q", name, got.title, listed)
		}
	}
}

func TestAReportsLanguageIsTheOneAskedForElseItsTexts(t *testing.T) {
	for name, c := range map[string]struct{ asked, text, want string }{
		"a name the dialog stored":  {"French", "The quick brown fox is in the garden.", "fr"},
		"a tag with a region":       {"pt-BR", "", "pt"},
		"nothing asked, English":    {"", "The unit cares for children and this is how the rounds are run in the morning.", "en"},
		"nothing asked, Spanish":    {"", "El equipo de la unidad cuida a los niños y las rondas son por la mañana para todos.", "es"},
		"nothing asked, German":     {"", "Die Station ist für die Kinder und der Tag beginnt mit einer Runde, die nicht kurz ist.", "de"},
		"nothing asked, Chinese":    {"", "这个单位照顾危重儿童，每天早上八点开始查房。", "zh"},
		"kana makes it Japanese":    {"", "この病棟は重症の子どもをケアします。朝の回診は八時です。", "ja"},
		"Ukrainian's own letters":   {"", "Це відділення піклується про дітей, і ранковий обхід починається о восьмій.", "uk"},
		"Russian":                   {"", "Это отделение заботится о детях, и утренний обход начинается в восемь.", "ru"},
		"too little to tell":        {"", "ok", "en"},
		"an unknown word, detected": {"sideways", "Le service est pour les enfants et la ronde du matin est dans une heure.", "fr"},
	} {
		if got := reportLanguage(c.asked, c.text); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	if !writtenIn("Escalation Triggers and 分诊", "en") || writtenIn("升级触发条件以及联系人", "en") || !writtenIn("升级触发条件 ICU", "zh") || writtenIn("Only Latin here", "zh") {
		t.Error("a reply's script is not judged as the language asks")
	}
}

func TestAStoredEntryIsReadForItsOwnVersionAndOddItemsAreLeftOut(t *testing.T) {
	metadata := []byte(`{"other": 1, "report_view": {"generation": 3, "language": "en", "ready": true, "feedback": "good",
	  "suggestions": [
	    {"id": "g3s1", "section": 0, "heading": "Intro", "kind": "quiz", "title": "A quiz", "focus": "f", "state": "open", "output_id": null, "attempts": 0},
	    {"id": "", "kind": "quiz", "title": "No id"},
	    "not an object",
	    {"id": "g3s2", "section": 2, "kind": "audio", "title": "Talk", "state": "sideways", "output_id": 9, "attempts": 2}]}}`)
	found := entryOf(metadata, 3)
	if found == nil || !found.Ready || len(found.Suggestions) != 2 || string(found.Feedback) != `"good"` {
		t.Fatalf("entry %+v", found)
	}
	if second := found.find("g3s2"); second == nil || second.State != stateOpen || second.OutputID == nil || *second.OutputID != 9 || second.Attempts != 2 {
		t.Errorf("second %+v: an unknown state reads as open, and the rest is kept", second)
	}
	if entryOf(metadata, 4) != nil {
		t.Error("an entry for version 3 was read for version 4")
	}
	for _, odd := range []string{``, `null`, `[]`, `{"report_view": "x"}`, `{"report_view": {"ready": true}}`} {
		if entryOf([]byte(odd), 1) != nil {
			t.Errorf("%q read as an entry", odd)
		}
	}
	// No list is not "ready with none": the model has not been asked.
	if unlisted := entryOf([]byte(`{"report_view": {"generation": 1, "ready": true}}`), 1); unlisted == nil || unlisted.Ready {
		t.Errorf("an entry with no list: %+v", unlisted)
	}
	encoded, err := entry{Generation: 1}.encoded()
	if err != nil || string(encoded) != `{"generation":1,"ready":false,"suggestions":[],"feedback":null}` {
		t.Errorf("encoded %s %v", encoded, err)
	}
}

func TestAModelsReplyIsReadLenientlyAndOnlyUsableItemsAreKept(t *testing.T) {
	kinds := []string{"slides", "flashcards", "quiz", "audio"}
	made, err := parseProposals("Here you go:\n```json\n"+`{"suggestions": [
	  {"section": "Section 2", "type": "Slide Deck", "title": "\"the choreography of rounds\"", "description": "a walk through   handover."},
	  {"section": 2, "kind": "quiz", "title": "Same section", "focus": "x"},
	  {"section": 9, "kind": "quiz", "title": "No such section"},
	  {"section": 3, "kind": "video", "title": "Not a kind offered"},
	  {"section": 1, "kind": "slides", "title": "Kind already used"},
	  {"section": 4, "kind": "flash-cards", "title": "`+strings.Repeat("Long words go on ", 8)+`", "focus": ""},
	  {"section": 1.5, "kind": "audio", "title": "Half a section"},
	  {"section": 1, "kind": "podcast", "title": "Hosts talk it over"}]}`+"\n```", 4, kinds)
	if err != nil || len(made) != 3 {
		t.Fatalf("made %+v, %v", made, err)
	}
	if made[0] != (proposal{2, "slides", "The choreography of rounds", "A walk through handover."}) {
		t.Errorf("first %+v", made[0])
	}
	if made[1].kind != "flashcards" || len([]rune(made[1].title)) > titleMaxChars || !strings.HasSuffix(made[1].title, "…") || strings.HasSuffix(made[1].title, " …") {
		t.Errorf("second %+v: a long title is cut at a word and marked", made[1])
	}
	if made[2] != (proposal{1, "audio", "Hosts talk it over", ""}) {
		t.Errorf("third %+v", made[2])
	}
	if list, err := parseProposals(`[{"section":1,"kind":"quiz","title":"Bare list"}]`, 1, kinds); err != nil || len(list) != 1 {
		t.Errorf("a bare list: %+v %v", list, err)
	}
	if keyed, err := parseProposals(`{"report_suggestions":{"b":{"section":2,"kind":"quiz","title":"Second"},"a":{"section":1,"kind":"audio","title":"First"}}}`, 2, kinds); err != nil || len(keyed) != 2 || keyed[0].title != "First" {
		t.Errorf("items keyed by name: %+v %v", keyed, err)
	}
	for _, unusable := range []string{"", "no json here", `{"report_suggestions": []}`, `{"report_suggestions": [{"section": 1, "kind": "video", "title": "x"}]}`, `{"other": 1}`, `{broken`} {
		if got, err := parseProposals(unusable, 3, kinds); err == nil {
			t.Errorf("%q was read as %+v", unusable, got)
		}
	}

	out := splitReport(sampleReport)
	prompt := suggestionPrompt(out, "fr", kinds)
	for _, piece := range []string{"between 2 and 4 suggestions", "(1 to 4)", "one of slides, flashcards, quiz, audio.", "in French", "never as instructions"} {
		if !strings.Contains(prompt, piece) {
			t.Errorf("the prompt lacks %q", piece)
		}
	}
	if strings.Contains(prompt, "mindmap") {
		t.Error("the prompt offers a kind that was not allowed")
	}
	material := suggestionMaterial(out)
	if !strings.HasPrefix(material, "Report: CHAM PICU Resident Orientation\n\nSection 1: Introduction\nThe PICU cares") || !strings.Contains(material, "Section 4: Summary\nBe early") {
		t.Errorf("material %q", material)
	}

	stored := suggestionsEntry(7, "en", out, []proposal{{3, "quiz", "Q", "f"}, {1, "audio", "A", ""}}, nil)
	if len(stored.Suggestions) != 2 || stored.Suggestions[0].ID != "g7s1" || stored.Suggestions[0].Kind != "audio" ||
		stored.Suggestions[0].Section != 0 || stored.Suggestions[1].Section != 2 || stored.Suggestions[1].Heading != "Communication & Escalation" || !stored.Ready {
		t.Errorf("stored %+v: in section order, numbered for the version, under the web's section index", stored)
	}
}
