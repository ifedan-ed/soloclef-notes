package agentrules

import (
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/automationdrafts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
)

// A name listed twice would be two rows on the page for one mark.
func TestTheCatalogueNamesEachToolOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, listed := range catalogue {
		if listed.name == "" || listed.group == "" {
			t.Errorf("an entry without a name or a heading: %+v", listed)
		}
		if seen[listed.name] {
			t.Errorf("%s is listed twice", listed.name)
		}
		seen[listed.name] = true
	}
}

// A saved tool list is read as its names, each once, in the server's order;
// anything that is not a list of named tools lists nothing.
func TestAnAppsSavedListIsReadAsItsNames(t *testing.T) {
	for raw, want := range map[string][]string{
		`[{"name": "b"}, {"name": "a"}, {"name": "b"}, {"description": "x"}, {"name": ""}]`: {"b", "a"},
		`[]`:            {},
		`"not a list"`:  nil,
		`{"name": "a"}`: nil,
		`[{"name": 5}]`: nil,
		`not even JSON`: nil,
	} {
		if got := listedNames([]byte(raw)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %#v, want %#v", raw, got, want)
		}
	}
}

// A tool whose card is itself the question (an automation draft, a mail
// draft) has no "ask first" to offer: the page does not list it, and a mark
// for it cannot be set.
func TestTheDraftingToolsCannotBeMarked(t *testing.T) {
	for _, name := range []string{automationdrafts.ToolName, mailtools.DraftTool} {
		if inCatalogue(name) {
			t.Errorf("%s is listed on the page", name)
		}
	}
	// The mailbox's reads stay markable: only the draft left the list.
	if !inCatalogue(mailtools.ReadTool) {
		t.Errorf("%s is no longer listed", mailtools.ReadTool)
	}
}
