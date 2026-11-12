package accountprofile

import (
	"strings"
	"testing"
)

// The settings change is a partial update: what is not mentioned is not
// written, and the three ways a body can be wrong are each told to the client.
func TestReadSettingsChanges(t *testing.T) {
	const json = "application/json"

	t.Run("only what is mentioned is changed", func(t *testing.T) {
		changes, failures, err := ReadSettingsChanges(json, []byte(`{"api_access_enabled":true}`))
		if err != nil || len(failures) > 0 {
			t.Fatalf("refused a good body: %v %v", failures, err)
		}
		if changes.APIAccessEnabled == nil || !*changes.APIAccessEnabled {
			t.Errorf("api_access_enabled = %v, want true", changes.APIAccessEnabled)
		}
		if changes.ChatInstructions != nil || changes.CitationsEnabled != nil {
			t.Errorf("settings that were not mentioned would be written: %+v", changes)
		}
	})

	t.Run("an empty body changes nothing", func(t *testing.T) {
		changes, failures, err := ReadSettingsChanges(json, []byte(`{}`))
		if err != nil || len(failures) > 0 || changes.Mentions() {
			t.Fatalf("got %+v %v %v, want no change", changes, failures, err)
		}
	})

	t.Run("null clears the instructions", func(t *testing.T) {
		changes, failures, err := ReadSettingsChanges(json, []byte(`{"chat_instructions":null}`))
		if err != nil || len(failures) > 0 {
			t.Fatalf("refused a clear: %v %v", failures, err)
		}
		if changes.ChatInstructions == nil || *changes.ChatInstructions != "" {
			t.Errorf("chat_instructions = %v, want the empty text", changes.ChatInstructions)
		}
	})

	t.Run("a switch cannot be null", func(t *testing.T) {
		_, failures, err := ReadSettingsChanges(json, []byte(`{"citations_enabled":null,"api_access_enabled":null}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(failures) != 2 || failures[0].Location[1] != "citations_enabled" ||
			failures[1].Location[1] != "api_access_enabled" {
			t.Fatalf("failures = %+v, want one per switch", failures)
		}
	})

	t.Run("a switch that is not a boolean is refused once", func(t *testing.T) {
		_, failures, _ := ReadSettingsChanges(json, []byte(`{"api_access_enabled":[]}`))
		if len(failures) != 1 {
			t.Fatalf("failures = %+v, want exactly one", failures)
		}
	})

	t.Run("the instructions are bounded", func(t *testing.T) {
		long := strings.Repeat("a", InstructionsMaxLength+1)
		_, failures, _ := ReadSettingsChanges(json, []byte(`{"chat_instructions":"`+long+`"}`))
		if len(failures) != 1 || failures[0].Location[1] != "chat_instructions" {
			t.Fatalf("failures = %+v, want the length refused", failures)
		}
		atLimit := strings.Repeat("a", InstructionsMaxLength)
		if _, failures, _ := ReadSettingsChanges(json, []byte(`{"chat_instructions":"`+atLimit+`"}`)); len(failures) > 0 {
			t.Fatalf("refused instructions at the limit: %+v", failures)
		}
	})

	t.Run("an unknown setting is refused", func(t *testing.T) {
		// The old name, which a client not yet updated would send.
		_, failures, _ := ReadSettingsChanges(json, []byte(`{"qna_custom_instructions":"x"}`))
		if len(failures) != 1 || failures[0].Location[1] != "qna_custom_instructions" {
			t.Fatalf("failures = %+v, want the unknown key named", failures)
		}
	})
}
