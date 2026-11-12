package podcast

import (
	"strings"
	"testing"
)

func TestABriefIsReadStrictly(t *testing.T) {
	brief, err := ReadBrief([]byte(twoSpeakerBrief))
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Speakers) != 2 || *brief.Speakers[0].Delivery != "Bright." || brief.Duration.MaxSeconds != 300 {
		t.Fatalf("read %+v", brief)
	}
	refused := map[string]string{
		"null":           `null`,
		"unknown key":    strings.Replace(twoSpeakerBrief, `"focus":null`, `"focus":null,"mood":"x"`, 1),
		"no speakers":    `{"language":"en","speakers":[],"duration":{"min_seconds":20,"max_seconds":30}}`,
		"reversed range": strings.Replace(twoSpeakerBrief, `"min_seconds":120`, `"min_seconds":900`, 1),
		"shared slot":    strings.Replace(twoSpeakerBrief, `"slot":1`, `"slot":0`, 1),
	}
	for name, column := range refused {
		if _, err := ReadBrief([]byte(column)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Rows from before 2026-09 give the length in minutes.
func TestABriefInMinutesIsStillRead(t *testing.T) {
	legacy := strings.Replace(twoSpeakerBrief, `"duration":{"min_seconds":120,"max_seconds":300}`, `"duration":{"min_minutes":2,"max_minutes":5}`, 1)
	brief, err := ReadBrief([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if brief.Duration != (Duration{MinSeconds: 120, MaxSeconds: 300}) {
		t.Fatalf("read %+v", brief.Duration)
	}
}

func TestTheOutlineIsSizedToTheBriefsCeiling(t *testing.T) {
	brief, _ := ReadBrief([]byte(twoSpeakerBrief))
	target, minimum, segments := sizes(brief)
	// 300s at 150 words a minute; 120s likewise; 750/250 beats.
	if target != 750 || minimum != 300 || segments != 3 {
		t.Fatalf("got %d, %d, %d", target, minimum, segments)
	}
	prompt := outlinePrompt(brief)
	for _, want := range []string{"up to about 750 words", "up to about 3 segments", "at least 300 words", "- slot 1 — Ben (role: guest)", `"target_words": 250`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the outline prompt lacks %q", want)
		}
	}
}

func TestEachBeatIsShownTheEndOfTheConversationSoFar(t *testing.T) {
	brief, _ := ReadBrief([]byte(twoSpeakerBrief))
	long := strings.Repeat("word ", 400)
	got := recap(brief, []Turn{{Speaker: 0, Text: "Opening."}, {Speaker: 1, Text: long + "end"}})
	if len([]rune(got)) != recapRunes || !strings.HasSuffix(got, "end") {
		t.Fatalf("the recap is %d runes ending %q", len([]rune(got)), got[len(got)-10:])
	}
	if prompt := segmentPrompt(brief, segment{Title: "Close", TalkingPoints: []string{"a"}, TargetWords: 90}, 3, 3, ""); !strings.Contains(prompt, "opening segment") {
		t.Fatal("a beat with no recap is not told it opens the conversation")
	}
}

func TestARepliedLineWithoutTextRefusesTheReply(t *testing.T) {
	if _, err := readTurns([]byte(`{"turns":[{"speaker":0,"text":"  "}]}`)); err == nil {
		t.Fatal("a blank line was accepted")
	}
	turns, err := readTurns([]byte(`{"turns":[{"speaker":1,"text":" Hi. ","tone":"x"}]}`))
	if err != nil || len(turns) != 1 || turns[0].Text != "Hi." {
		t.Fatalf("got %v, %v", turns, err)
	}
	plan, err := readOutline([]byte(`{"segments":[{"title":"A","talking_points":[],"target_words":0}]}`))
	if err != nil || plan.Segments[0].TargetWords != 1 {
		t.Fatalf("a beat sized at zero: %v, %v", plan, err)
	}
}
