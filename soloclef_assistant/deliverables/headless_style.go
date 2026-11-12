package deliverables

import "encoding/json"

// StyleAnswer is the reply to the infographic's style question that chooses
// the given style and orientation, in the shape the page's question card
// sends it, with the orientation beside it (the card asks only for a
// style, so a chat's answer has none).
//
// It exists for a run with nobody watching: a notebook's studio asks for
// the style and the orientation in its own dialog, before the run, so when
// the infographic pauses to ask, the runner answers with what the person
// already chose. Writing the answer here, beside chosenStyle which reads
// it, keeps the two in one place; a style or an orientation that does not
// exist is refused instead of drawn.
func StyleAnswer(style, orientation string) (json.RawMessage, error) {
	if style != autoStyle {
		if _, known := styleNamed(style); !known {
			return nil, &styleUnknown{style}
		}
	}
	if _, err := canvasNamed(orientation); err != nil {
		return nil, err
	}
	type answer struct {
		QuestionID string   `json:"question_id"`
		OptionIDs  []string `json:"option_ids"`
	}
	return json.Marshal(struct {
		Type          string   `json:"type"`
		PresetID      string   `json:"preset_id"`
		PresetVersion int      `json:"preset_version"`
		Orientation   string   `json:"orientation,omitempty"`
		Answers       []answer `json:"answers"`
	}{"respond", styleQuestionID, styleQuestionVersion, orientation, []answer{{styleQuestion, []string{style}}}})
}

// InfographicStyles is every style id an infographic may be asked for,
// "auto" first: what a dialog that asks the question itself may send.
func InfographicStyles() []string {
	ids := []string{autoStyle}
	for _, style := range visualStyles {
		ids = append(ids, style.id)
	}
	return ids
}

type styleUnknown struct{ style string }

func (e *styleUnknown) Error() string {
	return "\"" + e.style + "\" is not one of the infographic styles"
}
