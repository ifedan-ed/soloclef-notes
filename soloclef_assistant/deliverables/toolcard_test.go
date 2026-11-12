package deliverables

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func rendered(t *testing.T, payload any) string {
	t.Helper()
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A card is an allowlist. What it is handed is saved in the message and can
// be published, so a field a tool adds to its result later must not ride
// along by default.
func TestTheCardsSendOnlyWhatThePageDraws(t *testing.T) {
	saved := func(text string) event.Event { return event.Event{Kind: event.ToolResult, Tool: SaveTool, Text: text} }

	card, ok := ToolCard(saved(`{"status":"saved","artifact_id":41,"generation":1,"title":"Cells","format":"quiz","storage_key":"artifacts/u-1/41/x.json",` +
		`"files":[{"file_id":241,"role":"primary","filename":"cells.json","mime_type":"application/json","size_bytes":10,"checksum":"abc"}]}`))
	want := `{"status":"saved","artifact_id":41,"generation":1,"title":"Cells","format":"quiz","files":[{"file_id":241,"role":"primary","filename":"cells.json","mime_type":"application/json","size_bytes":10}]}`
	if !ok || rendered(t, card) != want {
		t.Errorf("a saved quiz\n got %s\nwant %s", rendered(t, card), want)
	}
	// A mind map has no file. The page reads "files" as a list or as
	// absent; null fails its schema and the card would not draw.
	for _, text := range []string{
		`{"status":"saved","artifact_id":42,"generation":1,"title":"Map","format":"mindmap","files":[]}`,
		`{"status":"saved","artifact_id":42,"generation":1,"title":"Map","format":"mindmap","files":null}`,
		`{"status":"saved","artifact_id":42,"generation":1,"title":"Map","format":"mindmap"}`,
	} {
		card, ok := ToolCard(saved(text))
		if !ok || rendered(t, card) != `{"status":"saved","artifact_id":42,"generation":1,"title":"Map","format":"mindmap","files":[]}` {
			t.Errorf("%s -> %s", text, rendered(t, card))
		}
	}

	podcast := func(text string, failed bool) event.Event {
		return event.Event{Kind: event.ToolResult, Tool: PodcastTool, Text: text, IsError: failed}
	}
	card, ok = ToolCard(podcast(`{"status":"awaiting_brief","podcast_id":12,"title":"Mitosis","message":"for the model","source_content":"private"}`, false))
	if !ok || rendered(t, card) != `{"status":"awaiting_brief","podcast_id":12,"title":"Mitosis"}` {
		t.Errorf("an opened podcast -> %s", rendered(t, card))
	}
	// A podcast that could not be opened shows why, in the person's words,
	// without the note that was for the model.
	card, ok = ToolCard(podcast("error: "+noSpeechService+refusedPodcastNote, true))
	if !ok || rendered(t, card) != `{"status":"failed","error":"`+noSpeechService+`"}` {
		t.Errorf("a refused podcast -> %s", rendered(t, card))
	}
	card, _ = ToolCard(podcast("error: "+strings.Repeat("é", 400), true))
	if failed := card.(PodcastCard); len([]rune(failed.Error)) != 300 {
		t.Errorf("a long error is %d characters on the card", len([]rune(failed.Error)))
	}

	// Everything else has no card here, and the caller shows its default.
	for name, result := range map[string]event.Event{
		"a save that failed":            {Tool: SaveTool, Text: "error: the deck was not saved: the title is empty", IsError: true},
		"a save that says it failed":    saved(`{"status":"failed","error":"x"}`),
		"a save with no artifact":       saved(`{"status":"saved","artifact_id":0}`),
		"a save that is not JSON":       saved(`saved!`),
		"a podcast with no id":          podcast(`{"status":"awaiting_brief","title":"T"}`, false),
		"a podcast with no status":      podcast(`{"podcast_id":3}`, false),
		"a podcast that is not JSON":    podcast(`done`, false),
		"the instructions":              {Tool: InstructionsTool, Text: flashcardInstructions},
		"another package's tool":        {Tool: "search_knowledge_base", Text: `{"status":"saved","artifact_id":41}`},
		"the specialist's own delegate": {Tool: DelegateName, Text: "Saved a deck."},
	} {
		if card, ok := ToolCard(result); ok || card != nil {
			t.Errorf("%s got a card: %+v", name, card)
		}
	}
}

// The kinds are declared once. Everything that names a kind to a model (the
// instructions tool, the two prompts, the delegate's description) or acts on
// one (the save tool's formats) is checked against that table here, so a
// kind cannot be offered that is not made, or made and never offered.
func TestEveryKindOfferedIsOneThatIsMade(t *testing.T) {
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	owner := Owner{UserID: "u-1"}
	instructions := s.instructionsTool(owner)
	// The built formats' instructions are the sandbox image's skills.
	l.shed.files = map[string][]byte{}
	for name := range documentFormats {
		l.shed.files["/opt/skills/"+name+"/SKILL.md"] = []byte("The " + name + " skill.")
	}
	for _, k := range kinds {
		text, err := instructions.Run(context.Background(), toolCall(`{"artifact_type":"`+k.name+`"}`))
		if _, built := documentFormats[k.name]; built {
			// Saved by save_artifact from a path, after verify_artifact:
			// the note that follows the skill says so, with the format.
			if err != nil || !strings.HasPrefix(text, "The "+k.name+" skill.") || !strings.Contains(text, SaveTool+`(format="`+k.name+`"`) ||
				!strings.Contains(text, VerifyTool) || !strings.Contains(saveParameters, `"`+k.name+`"`) || contentField[k.name] != "" || finishers[k.name] != "" || k.instructions != "" {
				t.Errorf("%s: instructions %q, %v", k.name, text, err)
			}
		} else if err != nil || text != k.instructions || text == "" {
			t.Errorf("%s: instructions %q, %v", k.name, text, err)
		}
		// The instructions name the tool that finishes the kind, and that
		// tool exists and takes the kind.
		finisher, own := finishers[k.name]
		if !own {
			finisher = SaveTool
		}
		if _, built := documentFormats[k.name]; built {
			// Checked above.
		} else if own {
			// A kind with a tool of its own is not also one of
			// save_artifact's formats: there would be two ways to make it.
			if contentField[k.name] != "" {
				t.Errorf("%s has its own tool and is a format save_artifact takes", k.name)
			}
		} else if contentField[k.name] == "" || !strings.Contains(text, `format "`+k.name+`"`) || !strings.Contains(saveParameters, `"`+k.name+`"`) {
			t.Errorf("%s is not a format save_artifact takes, or its instructions do not name it", k.name)
		}
		if !strings.Contains(text, finisher) {
			t.Errorf("%s: the instructions do not name %s", k.name, finisher)
		}
		for where, prompt := range map[string]string{"the main agent's block": ArtifactsPrompt(), "the delegate's description": delegateDescription} {
			if !strings.Contains(prompt, k.label) {
				t.Errorf("%s does not offer %s", where, k.label)
			}
		}
		if !strings.Contains(systemPrompt, "   - "+k.name+": ") {
			t.Errorf("the specialist's prompt does not list %s", k.name)
		}
		if unavailable[k.name] != "" {
			t.Errorf("%s is both made and refused", k.name)
		}
	}
	if len(contentField) != len(kinds)-len(finishers)-len(documentFormats) {
		t.Errorf("save_artifact takes %d formats for %d kinds", len(contentField), len(kinds))
	}
	// The main agent is told what to hand over as one phrase, and is told
	// the rest is not available rather than left to find out.
	block := ArtifactsPrompt()
	if !strings.HasPrefix(block, "The deliverables tool makes things the user can open and keep: "+kindLabels()+".") || !strings.Contains(kindLabels(), "a PDF report, a Word document, a slide deck, a spreadsheet, an interactive page") ||
		strings.Contains(block, "<artifacts>") || strings.HasSuffix(block, "\n") || !strings.Contains(block, "say plainly that it cannot be made") {
		t.Errorf("the main agent's block:\n%s", block)
	}

	// Every tool the nested agent is given is one a provider accepts.
	for _, built := range []tool.Tool{instructions, s.saveTool(owner), s.podcastTool(owner), s.imageTool(owner), s.videoTool(owner),
		s.executeTool(owner), s.readFileTool(owner), s.verifyTool(owner)} {
		if err := built.Validate(); err != nil {
			t.Error(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(built.Parameters, &schema); err != nil {
			t.Fatalf("%s: %v", built.Name, err)
		}
		for _, name := range schema.Required {
			if _, declared := schema.Properties[name]; !declared {
				t.Errorf("%s requires %s, which it does not declare", built.Name, name)
			}
		}
	}
	if delegate, err := s.Delegate(owner); err != nil || delegate.Name != DelegateName || delegate.Description != delegateDescription {
		t.Errorf("delegate = %+v, %v", delegate, err)
	}
}
