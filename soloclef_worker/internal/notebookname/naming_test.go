package notebookname

import (
	"testing"
	"time"
)

func TestATitleIsCleanedToAShortPlainTopic(t *testing.T) {
	cases := map[string]string{
		`Title: "**Management of neonatal sepsis**."`: "Management of Neonatal Sepsis",
		"🩺 the WHO guide to the ICU":                  "The WHO Guide to the ICU",
		"Untitled notebook":                           "",
		"   ":                                         "",
		"an extremely long title that keeps going well past the sixty character limit": "An Extremely Long Title That Keeps Going Well Past The",
	}
	for raw, want := range cases {
		if got := cleanTitle(raw, "en"); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", raw, got, want)
		}
	}
	// Another language keeps its own capitalisation.
	if got := cleanTitle("manejo de la sepsis neonatal", "es"); got != "manejo de la sepsis neonatal" {
		t.Errorf("a Spanish title was title-cased: %q", got)
	}
}

func TestTheReplyIsReadWithItsIcon(t *testing.T) {
	got := parseReply("Sure: {\"title\": \"heart failure\", \"icon\": \"🗂\"}", "en")
	if got.title != "Heart Failure" || got.icon != "🗂️" {
		t.Fatalf("got %+v", got)
	}
	if got := parseReply(`{"title":"x y","icon":"🦄"}`, "en"); got.icon != "" {
		t.Fatalf("an icon the dialog does not offer was kept: %+v", got)
	}
	if got := parseReply("\n  Cardiac Physiology\nmore", "en"); got.title != "Cardiac Physiology" {
		t.Fatalf("a bare line was not taken as the title: %+v", got)
	}
}

func TestAFileNameIsReadAsWords(t *testing.T) {
	if got := humanizeFilename("complications-of_premature-delivery.pptx"); got != "complications of premature delivery" {
		t.Fatalf("got %q", got)
	}
	if got := humanizeFilename("Already a title.v2"); got != "Already a title.v2" {
		t.Fatalf("got %q", got)
	}
}

func TestTheLanguageIsTheMaterials(t *testing.T) {
	cases := map[string]string{
		"The management of sepsis in children is the subject of this review.":         "en",
		"El manejo de la sepsis en los niños es el tema de esta revisión para todos.": "es",
		"新生儿败血症的管理是本综述的主题，我们讨论了诊断和治疗方法。":                                              "zh",
		"新生児敗血症の管理について、このレビューで説明します。":                                                 "ja",
		"Лечение сепсиса у детей является предметом этого обзора.":                    "ru",
		"Лікування сепсису у дітей є предметом цього огляду і їх.":                    "uk",
		"short": "en",
	}
	for text, want := range cases {
		if got := targetLanguage(text); got != want {
			t.Errorf("targetLanguage(%q) = %q, want %q", text, got, want)
		}
	}
	if inLanguage("新生儿败血症的管理", "en") || !inLanguage("Neonatal Sepsis", "en") || !inLanguage("新生儿败血症", "zh") {
		t.Fatal("a reply's script was misjudged")
	}
}

func TestANotebookIsNamedOnceThenRefinedOnlySoonAfterAndOnlyFromMoreSources(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	fresh := readState("auto", nil)
	if decide(fresh, 0, false, triggerSource, now) != "" {
		t.Fatal("named from nothing")
	}
	if decide(fresh, 0, true, triggerQuestion, now) != fromQuestion || decide(fresh, 1, true, triggerQuestion, now) != fromSources {
		t.Fatal("the first name is not from the sources when one is ready, else the question")
	}
	once := readState("auto", next(0, fromSources, 1, now.Add(-10*time.Minute)))
	if decide(once, 3, false, triggerSource, now) != fromSources {
		t.Fatal("more sources soon after did not refine the name")
	}
	if decide(once, 1, false, triggerSource, now) != "" || decide(once, 3, true, triggerQuestion, now) != "" {
		t.Fatal("a refinement without more sources, or from a question")
	}
	if decide(once, 3, false, triggerSource, now.Add(time.Hour)) != "" {
		t.Fatal("refined after the window")
	}
	if decide(readState("auto", next(1, fromSources, 3, now)), 9, false, triggerSource, now) != "" {
		t.Fatal("named a third time")
	}
	if decide(readState("user", nil), 3, true, triggerSource, now) != "" {
		t.Fatal("a notebook the person named was renamed")
	}
	if readState("auto", []byte(`{"runs":"lots"}`)).runs != 0 {
		t.Fatal("a column of the wrong shape was not read as never named")
	}
}
