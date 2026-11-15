package mailtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

// runCited calls a tool as a turn does, with the turn's registry.
func runCited(t *testing.T, tools *Tools, registry *citation.Registry, name, args string) string {
	t.Helper()
	for _, made := range tools.ForTurn(ada) {
		if made.Name == name {
			result, err := made.Run(context.Background(), tool.Call{Name: name, Args: json.RawMessage(args), Citations: registry})
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("no tool named %s", name)
	return ""
}

func TestMessagesAreCitedLikeWebPages(t *testing.T) {
	tools, post, _ := stocked()
	// The invoice as the search lists it and as a read returns it: one
	// message, so one source.
	post.found["home"][1].Subject = "Invoice"
	registry := citation.NewRegistry()

	listed := runCited(t, tools, registry, SearchTool, `{"account_id":"home"}`)
	if !strings.Contains(listed, "- [1] received 2026-03-11 09:00 UTC, unread | from Carol") ||
		!strings.Contains(listed, "- [2] received 2026-03-03 09:00 UTC | from books@example.org | subject: Invoice") {
		t.Fatalf("the search's messages are not numbered:\n%s", listed)
	}
	read := runCited(t, tools, registry, ReadTool, `{"account_id":"home","folder":"INBOX","uid":4}`)
	if !strings.HasPrefix(read, "<email>\n[2] One message from the user's mailbox") {
		t.Fatalf("reading the listed message did not keep its number:\n%s", read)
	}

	sources := registry.Sources()
	if len(sources) != 2 {
		t.Fatalf("want two sources, got %+v", sources)
	}
	for i, want := range []string{"Carol <carol@example.org> · Lunch · 2026-03-11", "books@example.org · Invoice · 2026-03-03"} {
		kind, label, ok := cite.LabelOf(sources[i].Ref)
		if !ok || kind != cite.Mail || sources[i].Kind != cite.Mail || label != want {
			t.Errorf("source %d is %q %q %v, want the mail label %q", i+1, kind, label, ok, want)
		}
	}
}

func TestALabelNamesWhatIsMissing(t *testing.T) {
	if got := mailLabel("  ", "", at(1).AddDate(0, 0, -1)); got != "Unknown sender · (no subject) · 2026-02-28" {
		t.Fatalf("got %q", got)
	}
}
