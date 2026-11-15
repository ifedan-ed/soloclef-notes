package prompts

import (
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

func moment(t *testing.T, text string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// filledName is a name that fills varchar(200) with em dashes, so a renderer
// that counted bytes anywhere would be two hundred characters out.
var filledName = strings.Repeat("—", 200)

// library is one account's prompts in the order the query returns them:
// created_at descending, and then the id, which is what breaks the tie
// between prompts 5 and 6 — both written at 10:00:04.
func library(t *testing.T) []Prompt {
	return []Prompt{
		{ID: 8, Name: "A default the account was given", Prompt: "Plan my week.",
			Mode: "transform", Version: 4, CreatedAt: moment(t, "2026-11-01T10:00:06Z")},
		{ID: 7, Name: "Edited twice", Prompt: "Tighten {selection} to one paragraph.",
			Mode: "transform", Version: 2, CreatedAt: moment(t, "2026-11-01T10:00:05Z")},
		{ID: 6, Name: "Tied, written second", Prompt: "Two.",
			Mode: "explore", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:04Z")},
		{ID: 5, Name: "Tied, written first", Prompt: "One.",
			Mode: "transform", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:04Z")},
		{ID: 4, Name: filledName, Prompt: "A name that fills the column.",
			Mode: "transform", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:03Z")},
		{ID: 3, Name: "A third prompt", Prompt: "Open to keys.",
			Mode: "transform", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:02Z")},
		{ID: 2, Name: "Explore — “deeply” & <html>", Prompt: "What else is here? Keep going.",
			Mode: "explore", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:01.5Z")},
		{ID: 1, Name: "Summarise this", Prompt: "Summarise {selection} in three bullets.",
			Mode: "transform", Version: 3, CreatedAt: moment(t, "2026-11-01T10:00:00Z")},
	}
}

// testdata/library.json is the list's expected body. It began as the Python
// tier's answer for the same rows; prompts stopped being shared on
// 2026-11-13, and the sharing flag is always false, kept for the installed
// phone bundles that require it (installed-builds: prompts-is-public).
//
// What it pins that no happy-path test would: the em dash, the curly quotes
// and the angle brackets going out unescaped; U+2028 raw inside a string; a
// half-second created_at written `.500000` and a whole one with no fraction,
// which is pydantic's spelling and not isoformat's; the order of the two
// prompts tied on created_at; and is_public false on every prompt.
func TestTheLibraryIsRenderedAsTheAppsReadIt(t *testing.T) {
	want, err := os.ReadFile("testdata/library.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonjson.Marshal(BuildList(library(t)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the library is not the expected bytes\n got %s\nwant %s", got, want)
	}
}

// An account with no prompts is an empty array, not null.
func TestNoPromptsIsAnEmptyArray(t *testing.T) {
	body, err := pythonjson.Marshal(BuildList(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "[]" {
		t.Errorf("got %s, want []", body)
	}
}

// The delete is the one route with no response_model, so its body is
// Starlette's own json.dumps of `{"success": True}`.
func TestTheDeletesAnswerIsOneKey(t *testing.T) {
	body, err := pythonjson.Marshal(deleted())
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"success":true}` {
		t.Errorf("got %s", body)
	}
}
