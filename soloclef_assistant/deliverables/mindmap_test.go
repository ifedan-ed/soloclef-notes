package deliverables

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
)

const photosynthesis = "# Photosynthesis\n\n- Light reactions\n  - Photosystem II\n  - Photosystem I\n- Calvin cycle\n  - Carbon fixation\n"

// Whatever bullets, indentation and line endings the model used, the map is
// stored in one spelling: the one the viewer's Markdown parser reads as the
// tree that was meant.
func TestAMindMapIsStoredInOneSpelling(t *testing.T) {
	for name, written := range map[string]string{
		"as stored":                     photosynthesis,
		"four spaces and other bullets": "# Photosynthesis\n* Light reactions\n    + Photosystem II\n    + Photosystem I\n* Calvin cycle\n    - Carbon fixation",
		"three spaces, Windows endings": "\r\n#   Photosynthesis  \r\n\r\n- Light reactions\r\n   - Photosystem II\r\n   - Photosystem I\r\n\r\n- Calvin cycle\r\n   - Carbon fixation\r\n\r\n",
	} {
		mindMap, err := NewMindMap(" Photosynthesis ", written)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if mindMap.Markdown != photosynthesis || mindMap.Title != "Photosynthesis" || mindMap.Nodes != 6 || mindMap.Depth != 3 {
			t.Errorf("%s: %+v", name, mindMap)
		}
	}
	// Emphasis, inline code and mathematics are words; so is a label that
	// merely starts with a hyphen or a number.
	kept := "# **Bold** root\n\n- `code` label\n- *emphasis*\n- -5 degrees\n- 1920s boom\n- 3.5 metres\n"
	if mindMap, err := NewMindMap("T", kept); err != nil || mindMap.Markdown != kept {
		t.Errorf("labels that are words: %+v, %v", mindMap, err)
	}
}

// deep is a chain of n nodes, each under the last.
func deep(n int) string {
	var b strings.Builder
	b.WriteString("# Root\n")
	for level := 0; level < n-1; level++ {
		b.WriteString(strings.Repeat("  ", level) + "- Level\n")
	}
	return b.String()
}

// wide is a root with n-1 items under it.
func wide(n int) string {
	return "# Root\n" + strings.Repeat("- Item\n", n-1)
}

func TestAMindMapIsChecked(t *testing.T) {
	// The limits themselves are allowed: sixty nodes, six levels, 120
	// characters.
	for name, written := range map[string]string{
		"sixty nodes": wide(60), "six levels": deep(6), "a label of 120 characters": "# Root\n- " + strings.Repeat("é", 120),
	} {
		if _, err := NewMindMap("T", written); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]struct{ title, markdown, says string }{
		"nothing":                     {"T", " \n\n", "the mind map is empty"},
		"no title":                    {"", photosynthesis, "the title is empty"},
		"no heading":                  {"T", "- A\n- B\n", `line 1 must be the root, written "# " and its label`},
		"a level-two heading":         {"T", "## Root\n- A\n", "line 1 must be the root"},
		"a heading with no label":     {"T", "# \n- A\n", "the mind map was not saved: line 1: the root is empty"},
		"a root alone":                {"T", "# Root\n", "the root has no items under it"},
		"a second heading":            {"T", "# Root\n- A\n## More\n- B\n", "line 3 is a second heading"},
		"a paragraph":                 {"T", "# Root\nSome prose.\n- A\n", `line 2 must be a list item, written "- " and its label, indented with spaces`},
		"a numbered list":             {"T", "# Root\n1. First\n", "line 2 must be a list item"},
		"a tab for indentation":       {"T", "# Root\n- A\n\t- B\n", "line 3 must be a list item"},
		"a bullet with no label":      {"T", "# Root\n- A\n- \n", "line 3 must be a list item"},
		"the first item indented":     {"T", "# Root\n  - A\n", "line 2 is indented, but the first item under the root starts at the margin"},
		"one space of indentation":    {"T", "# Root\n- A\n - B\n", "line 3 is indented 1 spaces; use 2 spaces per level"},
		"indentation between levels":  {"T", "# Root\n- A\n  - B\n   - C\n", "line 4 is indented 3 spaces, which is not a whole number of levels (2 spaces each)"},
		"a skipped level":             {"T", "# Root\n- A\n  - B\n- C\n      - D\n", "line 5 skips a level"},
		"sixty-one nodes":             {"T", wide(61), "at most 60 nodes including the root; this one has 61"},
		"seven levels":                {"T", deep(7), "at most 6 levels deep including the root; this one is 7"},
		"a label too long":            {"T", "# Root\n- " + strings.Repeat("a", 121), "line 2: the label is longer than 120 characters"},
		"a root too long":             {"T", "# " + strings.Repeat("a", 121) + "\n- A", "line 1: the root is longer than 120 characters"},
		"a link":                      {"T", "# Root\n- [site](https://example.org)\n", "line 2: the label contains a link, an image or HTML"},
		"a reference link":            {"T", "# Root\n- [site][1]\n", "contains a link, an image or HTML"},
		"an image":                    {"T", "# Root\n- ![](x.png)\n", "contains a link, an image or HTML"},
		"HTML":                        {"T", "# Root\n- <b>bold</b>\n", "contains a link, an image or HTML"},
		"a code fence":                {"T", "# Root\n- ```go\n", "contains a code fence or a directive"},
		"a numbered list in an item":  {"T", "# Root\n- 1. Steps\n", "line 2: the label begins with Markdown structure"},
		"a quotation in an item":      {"T", "# Root\n- > quoted\n", "begins with Markdown structure"},
		"a list in an item":           {"T", "# Root\n- - nested\n", "begins with Markdown structure"},
		"a heading in an item":        {"T", "# Root\n- # big\n", "begins with Markdown structure"},
		"a label of punctuation":      {"T", "# Root\n- **\n", "line 2: the label has no letters or digits"},
		"a control character":         {"T", "# Root\n- A\x07\n", "line 2 contains a control character (U+0007)"},
		"every problem in one answer": {"", "Root\n- A\n\t- B\n- [x](y)\n", "the mind map was not saved: the title is empty; line 1 must be the root, written \"# \" and its label; line 3 must be a list item, written \"- \" and its label, indented with spaces; line 4: the label contains a link, an image or HTML; write the words only"},
	} {
		_, err := NewMindMap(c.title, c.markdown)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The specialist handed a mind map: the artifact is its Markdown, as the
// document, with no file, and the card is told so with an empty list.
func TestTheSpecialistMakesAMindMapEndToEnd(t *testing.T) {
	l := &ledger{}
	args := func(markdown string) string {
		out, _ := json.Marshal(map[string]any{"format": "mindmap", "title": "Photosynthesis", "markdown": markdown})
		return string(out)
	}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"Mind map"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s1", Name: SaveTool, Args: args("# Photosynthesis\n- Light reactions\n      - Photosystem II\n")}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s2", Name: SaveTool, Args: args("# Photosynthesis\n* Light reactions\n    * Photosystem II\n    * Photosystem I\n* Calvin cycle\n    * Carbon fixation\n")}}},
		modeltest.Reply{Text: "Saved a mind map titled Photosynthesis with 6 nodes."},
	)
	s, _ := specialist(t, script, l)
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	delegate, err := s.Delegate(owner)
	if err != nil {
		t.Fatal(err)
	}
	var saved string
	if _, err := delegate.Agent.Start(context.Background(), "t", []model.Message{model.User("Map photosynthesis: ...")}, func(ev event.Event) {
		if ev.Kind == event.ToolResult && ev.Tool == SaveTool && !ev.IsError {
			saved = ev.Text
		}
	}); err != nil {
		t.Fatal(err)
	}
	requests := script.Requests()
	if told := requests[1].Messages[len(requests[1].Messages)-1].Content; !strings.Contains(told, `format "mindmap"`) {
		t.Errorf("the mind map instructions were %q", told)
	}
	if told := requests[2].Messages[len(requests[2].Messages)-1].Content; !strings.Contains(told, "line 3 is indented 6 spaces; use 2 spaces per level") {
		t.Errorf("after a badly indented map the model was told %q", told)
	}
	if saved != `{"status":"saved","artifact_id":41,"generation":1,"title":"Photosynthesis","format":"mindmap","files":[]}` {
		t.Fatalf("save result = %s", saved)
	}
	if len(l.created) != 1 {
		t.Fatalf("created = %d artifacts", len(l.created))
	}
	made := l.created[0]
	if made.Owner != owner || made.Format != "mindmap" || made.Markdown != photosynthesis || made.HasFile() || made.Data != nil || made.MimeType != "" ||
		string(made.Metadata) != `{"verification": {"verified": true, "reason": null}}` {
		t.Errorf("artifact = %+v", made)
	}
	if l.keys[0] != "" {
		t.Errorf("a file was stored for a mind map under %q", l.keys[0])
	}
	if len(l.indexed) != 1 || l.indexed[0] != 141 {
		t.Errorf("indexing was asked for %v", l.indexed)
	}
}
