package accesstokens

import (
	"os"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// testdata/tokens.json is the body the Python tier returned for GET /api/v1/pats
// against these rows, captured over HTTP. The labels are the seed's own: the
// curly quotes and the angle brackets are there because they decide the
// bytes — the Python renderer escapes none of them.
func TestTheKeyListIsThePythonTiersBytes(t *testing.T) {
	moment := func(text string) *time.Time {
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			panic(err)
		}
		return &parsed
	}
	tokens := []Token{
		{ID: 2, Label: "Second key — “quoted” & <tagged>", Prefix: "ss_pat_comparis",
			ExpiresAt: moment("2027-01-01T00:00:00Z"), LastUsedAt: moment("2026-11-01T00:00:00Z"),
			CreatedAt: *moment("2026-10-06T00:00:00Z")},
		{ID: 1, Label: "Owner key", Prefix: "ss_pat_comparis",
			CreatedAt: *moment("2026-10-05T00:00:00.25Z")},
	}

	want, err := os.ReadFile("testdata/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonjson.Marshal(BuildList(tokens))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the key list is not the captured bytes\n got %s\nwant %s", got, want)
	}
}

// An account with no keys is an empty array, not null.
func TestNoKeysIsAnEmptyArray(t *testing.T) {
	got, err := pythonjson.Marshal(BuildList(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[]" {
		t.Errorf("got %s, want []", got)
	}
}
