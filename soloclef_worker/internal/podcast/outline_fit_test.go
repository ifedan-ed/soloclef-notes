package podcast

import "testing"

func TestAnOutlineOverItsCeilingIsScaledDownAndOneWithinItIsKept(t *testing.T) {
	over := outline{Segments: []segment{{Title: "Open", TargetWords: 100}, {Title: "Core", TargetWords: 300}, {Title: "Close", TargetWords: 100}}}
	fitted := fitOutline(over, 250)
	total := 0
	for _, beat := range fitted.Segments {
		total += beat.TargetWords
	}
	if total > 250 || fitted.Segments[0].TargetWords != 50 || fitted.Segments[1].TargetWords != 150 || fitted.Segments[2].Title != "Close" {
		t.Errorf("fitted to %+v (total %d)", fitted.Segments, total)
	}
	if over.Segments[1].TargetWords != 300 {
		t.Error("fitting changed the plan it was given")
	}
	within := outline{Segments: []segment{{Title: "Only", TargetWords: 200}}}
	if got := fitOutline(within, 250); got.Segments[0].TargetWords != 200 {
		t.Errorf("a plan within its ceiling was changed: %+v", got.Segments)
	}
}
