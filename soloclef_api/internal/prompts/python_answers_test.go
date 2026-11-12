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

func author(name string) *string { return &name }

// filledName is the seed's prompt 4: a name that fills varchar(200) with em
// dashes, so a renderer that counted bytes anywhere would be two hundred
// characters out.
var filledName = strings.Repeat("—", 200)

// library is the recorded seed's prompts for the owner account, in the
// order the query returns them: created_at descending, and then the id, which
// is what breaks the tie between prompts 5 and 6 — both written at 10:00:04.
func library(t *testing.T) []Prompt {
	return []Prompt{
		{ID: 8, Name: "A default the account was given", Prompt: "Plan my week.",
			Mode: "transform", Version: 4, CreatedAt: moment(t, "2026-11-01T10:00:06Z")},
		{ID: 7, Name: "Published by the caller",
			Prompt: "The community list leaves this one out, because it is the caller's own.",
			Mode:   "transform", Version: 2, IsPublic: true,
			CreatedAt: moment(t, "2026-11-01T10:00:05Z")},
		{ID: 6, Name: "Tied, written second", Prompt: "Two.",
			Mode: "explore", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:04Z")},
		{ID: 5, Name: "Tied, written first", Prompt: "One.",
			Mode: "transform", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:04Z")},
		{ID: 4, Name: filledName, Prompt: "A name that fills the column.",
			Mode: "transform", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:03Z")},
		{ID: 3, Name: "A third prompt", Prompt: "Open to keys.",
			Mode: "transform", Version: 1,
			CreatedAt: moment(t, "2026-11-01T10:00:02Z")},
		{ID: 2, Name: "Explore — “deeply” & <html>", Prompt: "What else is here? Keep going.",
			Mode: "explore", Version: 1, CreatedAt: moment(t, "2026-11-01T10:00:01.5Z")},
		{ID: 1, Name: "Summarise this", Prompt: "Summarise {selection} in three bullets.",
			Mode: "transform", Version: 3,
			CreatedAt: moment(t, "2026-11-01T10:00:00Z")},
	}
}

// community is the same seed's published prompts as an account that has
// published none of them sees them — so all five, including the owner's. Two
// of the three publishers have a display name and one does not, which is the
// null author_name.
func community(t *testing.T) []Prompt {
	return []Prompt{
		{ID: 12, Name: "Community tied, second", Prompt: "Two.", Mode: "explore",
			Version: 1, IsPublic: true, CreatedAt: moment(t, "2026-11-01T10:00:09Z")},
		{ID: 11, Name: "Community tied, first", Prompt: "One.", Mode: "transform",
			Version: 1, IsPublic: true, CreatedAt: moment(t, "2026-11-01T10:00:09Z"),
			AuthorName: author("Sam Super")},
		{ID: 10, Name: "Community: no author name",
			Prompt: "The publisher has no display name, so author_name is null.",
			Mode:   "explore", Version: 1, IsPublic: true,
			CreatedAt: moment(t, "2026-11-01T10:00:08Z")},
		{ID: 9, Name: "Community: a week at a time", Prompt: "Plan a week from {selection}.",
			Mode: "transform", Version: 1, IsPublic: true,
			CreatedAt: moment(t, "2026-11-01T10:00:07Z"), AuthorName: author("Sam Super")},
		{ID: 7, Name: "Published by the caller",
			Prompt: "The community list leaves this one out, because it is the caller's own.",
			Mode:   "transform", Version: 2, IsPublic: true,
			CreatedAt: moment(t, "2026-11-01T10:00:05Z"), AuthorName: author("Ada Owner")},
	}
}

// testdata/library.json is the body the Python tier returned for
// GET /api/v1/prompts as the owner, against the seed rows of the Python-to-Go
// comparison, captured over HTTP from its own disposable world. Nothing in it
// is anybody's own text — every row of that seed is made up for the comparison
// — so there was nothing to replace.
//
// What it pins that no happy-path test would: the em dash, the curly quotes
// and the angle brackets going out unescaped; U+2028 raw inside a string; a
// half-second created_at written `.500000` and a whole one written `.000000`,
// which is pydantic's spelling and not isoformat's; and
// the order of the two prompts tied on created_at.
func TestTheLibraryIsThePythonTiersBytes(t *testing.T) {
	compare(t, "testdata/library.json", BuildList(library(t)))
}

// testdata/community.json is the same capture for GET /api/v1/prompts/public,
// as the scribe — an account that has published nothing, so it sees all five.
// It pins author_name's position (after every field PromptRead declares,
// because a pydantic subclass's own field comes last) and the null one.
func TestTheCommunityShelfIsThePythonTiersBytes(t *testing.T) {
	compare(t, "testdata/community.json", BuildPublicList(community(t)))
}

func compare(t *testing.T, path string, rendered []any) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonjson.Marshal(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is not the captured bytes\n got %s\nwant %s", path, got, want)
	}
}

// An account with no prompts is an empty array, not null — both ways round,
// because the two lists are rendered by two functions.
func TestNoPromptsIsAnEmptyArray(t *testing.T) {
	for _, rendered := range [][]any{BuildList(nil), BuildPublicList(nil)} {
		body, err := pythonjson.Marshal(rendered)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "[]" {
			t.Errorf("got %s, want []", body)
		}
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
