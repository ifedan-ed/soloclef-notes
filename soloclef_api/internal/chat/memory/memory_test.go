package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/markdownmemory"
)

func TestBracketsThePromptReplacedAreWrittenBack(t *testing.T) {
	// The stored document has no replacement characters, so every one in
	// the model's text is the prompt's.
	if got := restored("## Facts\n- 2026-01-02: uses Vec‹T› a lot", "## Facts\n- 2026-01-01: likes Go"); got != "## Facts\n- 2026-01-02: uses Vec<T> a lot" {
		t.Fatalf("restored %q", got)
	}
	// The stored document has its own: nothing can be told apart, so
	// nothing is changed.
	written := "## Facts\n- 2026-01-02: quotes with ‹ and ›"
	if got := restored(written, "## Facts\n- 2026-01-01: quotes with ‹"); got != written {
		t.Fatalf("a document with its own guillemets was rewritten: %q", got)
	}
}

func TestTheCardNeverCarriesTheDocument(t *testing.T) {
	document := "## Facts\n- 2026-01-01: SECRET"
	text, err := answer(result{Status: statusError, Message: "changed", Next: "merge", Current: &document})
	if err != nil {
		t.Fatal(err)
	}
	payload, mine := Card(event.Event{Tool: ToolName, Text: text})
	if !mine {
		t.Fatal("the memory card did not claim its own result")
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), "merge") {
		t.Fatalf("the card carries what is for the model alone: %s", encoded)
	}
	if string(encoded) != `{"status":"error","message":"changed"}` {
		t.Fatalf("card %s", encoded)
	}

	// A failed call shows a fixed sentence, whatever its text.
	payload, _ = Card(event.Event{Tool: ToolName, IsError: true, Text: "pq: relation \"user\" SECRET"})
	encoded, _ = json.Marshal(payload)
	if string(encoded) != `{"status":"error","message":"The memory could not be updated."}` {
		t.Fatalf("a failed call's card: %s", encoded)
	}
	// So does a result that is not this tool's JSON.
	payload, _ = Card(event.Event{Tool: ToolName, Text: "SECRET"})
	if encoded, _ = json.Marshal(payload); strings.Contains(string(encoded), "SECRET") {
		t.Fatalf("an unreadable result reached the card: %s", encoded)
	}
	if _, mine := Card(event.Event{Tool: "search_knowledge_base", Text: text}); mine {
		t.Fatal("the memory card claimed another tool's result")
	}
}

func TestTheSavedCardIsWhatThePageDraws(t *testing.T) {
	text, _ := answer(result{Status: statusSaved, Message: "Memory updated.", Warning: "near"})
	payload, _ := Card(event.Event{Tool: ToolName, Text: text})
	encoded, _ := json.Marshal(payload)
	// user-memory.tsx: status "saved" | "error", message, warning.
	if string(encoded) != `{"status":"saved","message":"Memory updated.","warning":"near"}` {
		t.Fatalf("card %s", encoded)
	}
}

// There is one memory, the asker's: the prompt's instructions and the tool
// as the model is offered it both name that block, and neither speaks of a
// team's or a workspace's.
func TestTheModelIsToldOfOneMemoryTheUsers(t *testing.T) {
	for name, text := range map[string]string{"the protocol": Protocol, "the description": description} {
		if !strings.Contains(text, "<user_memory>") {
			t.Errorf("%s does not name the block the prompt shows:\n%s", name, text)
		}
		for _, gone := range []string{"team", "workspace", "shared"} {
			if strings.Contains(strings.ToLower(text), gone) {
				t.Errorf("%s speaks of a %s:\n%s", name, gone, text)
			}
		}
	}
}

func TestOnlyADocumentPastTheSoftLimitWarns(t *testing.T) {
	if nearLimit(strings.Repeat("a", markdownmemory.SoftLimit)) != "" {
		t.Fatal("a document at the soft limit warned")
	}
	if got := nearLimit(strings.Repeat("é", markdownmemory.SoftLimit+1)); !strings.HasPrefix(got, "Memory is at 18001 of 25000 characters.") {
		t.Fatalf("warning %q", got)
	}
	if got := Usage("héllo"); got != "5 of 25000 characters used" {
		t.Fatalf("usage %q", got)
	}
}
