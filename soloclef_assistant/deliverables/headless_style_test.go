package deliverables

import (
	"strings"
	"testing"
)

// The answer a headless run gives must be one chosenStyle reads back as the
// same style and canvas: the two are written apart and would otherwise
// drift.
func TestAStyleAnswerIsReadBackAsItsStyle(t *testing.T) {
	for _, style := range InfographicStyles() {
		for _, orientation := range append([]string{""}, InfographicOrientations()...) {
			answer, err := StyleAnswer(style, orientation)
			if err != nil {
				t.Fatalf("%s %s: %v", style, orientation, err)
			}
			chosen, err := chosenStyle(answer)
			if err != nil || chosen.style != style || chosen.canvas.id != orientation {
				t.Errorf("%s %s was read back as %+v, %v", style, orientation, chosen, err)
			}
		}
	}
	if _, err := StyleAnswer("oil-painting", ""); err == nil {
		t.Error("a style that does not exist was answered")
	}
	if _, err := StyleAnswer("auto", "panorama"); err == nil {
		t.Error("an orientation that does not exist was answered")
	}
	if len(InfographicStyles()) < 2 || InfographicStyles()[0] != "auto" {
		t.Errorf("the styles should begin with auto: %v", InfographicStyles())
	}
	if got := strings.Join(InfographicOrientations(), " "); got != "landscape portrait square" {
		t.Errorf("the orientations, default first: %s", got)
	}
}
