package chat

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/drivetools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/nextcloudtools"
)

// The composer's tool list is the account's: the chat's own switches, then
// each connection's tools under its name, exactly the tools a turn of that
// account is offered, so every switch reaches a tool that exists.
func TestTheToolMenuListsWhatTheAccountsTurnsAreOffered(t *testing.T) {
	script := modeltest.NewScript()
	closed := 0
	apps := someApps{appSet{tools: []tool.Tool{notes(tool.Ask, new(atomic.Int64))}, apps: map[string]string{"write_note": "Notebook  helper"}, closed: &closed}}
	built := withAccounts(t, script, newRows(), apps)

	listed, err := pickerTools(built)(context.Background(), ada)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	for _, entry := range listed {
		if !entry.EnabledByDefault || entry.Description == "" || (entry.Group == "") != (entry.Connector == "") {
			t.Errorf("an entry the menu cannot show: %+v", entry)
		}
		groups[entry.Group+"/"+entry.Connector] = append(groups[entry.Group+"/"+entry.Connector], entry.Name)
	}
	for key, want := range map[string][]string{
		"/":                         namesOf(assembly.SwitchableTools()),
		"Mail/mail":                 mailtools.Names(),
		"Google Drive/google_drive": drivetools.Names(),
		"Nextcloud/nextcloud":       nextcloudtools.Names(),
		"Notebook helper/mcp":       {"write_note"},
	} {
		if strings.Join(groups[key], " ") != strings.Join(want, " ") {
			t.Errorf("%s lists %v, want %v", key, groups[key], want)
		}
	}
	// A line for a person, not the model's instructions.
	for _, entry := range listed {
		if entry.Name == mailtools.SearchTool && entry.Description != "Search the user's connected mailboxes, read-only" {
			t.Errorf("the mail search is described %q", entry.Description)
		}
	}
	// Listing opened nothing that stays open.
	if closed != 1 {
		t.Errorf("the connected apps were closed %d times", closed)
	}

	// Dee connected nothing: the chat's own switches alone.
	listed, err = pickerTools(built)(context.Background(), dee)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range listed {
		if entry.Group != "" && entry.Connector != "mcp" {
			t.Errorf("an account with nothing connected lists %+v", entry)
		}
	}
}

func namesOf(listed []assembly.Switchable) []string {
	names := make([]string, 0, len(listed))
	for _, entry := range listed {
		names = append(names, entry.Name)
	}
	return names
}
