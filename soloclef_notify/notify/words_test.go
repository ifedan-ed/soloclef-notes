package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

// A growing group counts every event, keeps the latest few names, and says
// only the count in the push: the names are the inbox's.
func TestAGroupGrowsByOneAndKeepsTheLatestNames(t *testing.T) {
	first := sourceEvent(SourceOutcome{DocumentID: 1, Failed: true, Reason: "unreadable"},
		SourceDocumentRow{UserID: "u", Title: "a.pdf", DocumentType: "FILE"})
	if first.title != "a.pdf could not be added" || first.push.Title != "An import failed" {
		t.Fatalf("first: %q / %q", first.title, first.push.Title)
	}
	stored, _ := json.Marshal(map[string]any{"count": 1, "names": []string{"a.pdf"}})
	for i, name := range []string{"b.pdf", "c.pdf", "d.pdf", "e.pdf", "f.pdf", "g.pdf"} {
		next := sourceEvent(SourceOutcome{DocumentID: int64(i + 2), Failed: true},
			SourceDocumentRow{UserID: "u", Title: name, DocumentType: "FILE"})
		grown := next.grow(stored)
		stored, _ = grown.encodedMetadata()
		if want := i + 2; grown.metadata["count"] != want {
			t.Fatalf("after %s: count %v, want %d", name, grown.metadata["count"], want)
		}
	}
	var final struct {
		Count int      `json:"count"`
		Names []string `json:"names"`
		Push  push     `json:"push"`
	}
	if err := json.Unmarshal(stored, &final); err != nil {
		t.Fatal(err)
	}
	if final.Count != 7 || len(final.Names) != keptNames || final.Names[0] != "c.pdf" || final.Names[4] != "g.pdf" {
		t.Errorf("final = %+v", final)
	}
	if final.Push.Title != "7 imports failed" || strings.Contains(final.Push.Title, "pdf") || final.Push.Body != "" {
		t.Errorf("the push says %+v", final.Push)
	}
	words := failedSources(7, final.Names)
	if words.title != "7 imports failed" || words.message != "c.pdf, d.pdf, e.pdf, f.pdf, g.pdf and 2 more could not be indexed." {
		t.Errorf("words = %+v", words)
	}
}

func TestAListOfFewNamesReadsAsASentence(t *testing.T) {
	if got := listed(2, []string{"a", "b"}); got != "a and b" {
		t.Errorf("%q", got)
	}
	if got := listed(3, []string{"a", "b", "c"}); got != "a, b and c" {
		t.Errorf("%q", got)
	}
	if got := listed(1, []string{" "}); got != "A source" {
		t.Errorf("an empty name: %q", got)
	}
	long := strings.Repeat("é", 300)
	if got := clipTitle(long); len([]rune(got)) != 121 {
		t.Errorf("a long name is %d characters", len([]rune(got)))
	}
}

// A Studio push says the kind and the notebook, opens the notebook's
// Studio, and on a failure offers Try again; the output's own title and
// the reason stay in the inbox.
func TestAStudioOutputSaysTheKindAndTheNotebookOnly(t *testing.T) {
	notebook, kind, title, nbTitle := int64(12), "slides", "Week 3 secrets", "Pharmacology"
	ready := studioEvent(StudioRun{UserID: "u", JobID: 4}, studioOutput{
		runTitle: "run", runKind: "artifact", notebookID: &notebook, outputKind: &kind, outputTitle: &title, notebookTitle: &nbTitle,
	})
	if ready.push != (push{Kind: "output", Title: "Slide deck ready", Body: "Pharmacology", Path: "/notebook/12?pane=studio"}) {
		t.Errorf("ready push = %+v", ready.push)
	}
	if ready.message != "“Week 3 secrets” is ready in Pharmacology." || ready.metadata["retry"] != false {
		t.Errorf("ready inbox = %q, %v", ready.message, ready.metadata["retry"])
	}
	failed := studioEvent(StudioRun{UserID: "u", PodcastID: 4, Failed: true, Reason: "The speech service did not answer."},
		studioOutput{runTitle: "Episode", runKind: "audio"})
	if failed.push != (push{Kind: "output", Title: "Audio overview could not be made", Path: "/inbox"}) {
		t.Errorf("failed push = %+v", failed.push)
	}
	if failed.metadata["retry"] != true || failed.metadata["status"] != "failed" ||
		failed.message != "“Episode” could not be made. The speech service did not answer." {
		t.Errorf("failed inbox = %q %v", failed.message, failed.metadata)
	}
	chat := studioEvent(StudioRun{UserID: "u", JobID: 4}, studioOutput{runTitle: "x", runKind: "artifact"})
	if chat.push.Title != "Output ready" || chat.metadata["output_kind"] != "artifact" {
		t.Errorf("a chat's artifact: %+v %v", chat.push, chat.metadata["output_kind"])
	}
}

// A collapsed row never carries a push of the person's own words.
func TestPushesNeverCarryThePersonsOwnWords(t *testing.T) {
	ready := readySources(1, []string{"My private diary.pdf"})
	if strings.Contains(ready.pushTitle, "diary") {
		t.Errorf("push title %q", ready.pushTitle)
	}
	for _, status := range []string{"succeeded", "failed", "needs_attention", "timed_out"} {
		words := automationWords[status]
		if words.push == "" || strings.Contains(words.push, "“") {
			t.Errorf("%s: %q", status, words.push)
		}
	}
}
