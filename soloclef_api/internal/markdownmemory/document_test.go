package markdownmemory

import (
	"encoding/json"
	"os"
	"testing"
)

// capture is one document asked of the running Python tier through
// PUT /api/v1/users/me/memory, and the memory_md it answered with — or the message it refused with.
//
// testdata/documents.json holds them. Nothing in it is an account's own text:
// every document was written for the comparison against the seeded database
// described in custom/docs/go-api-tier.md.
type capture struct {
	Name     string `json:"name"`
	Stored   string `json:"stored"`
	Sent     string `json:"sent"`
	Answered string `json:"answered"`
	Refusal  string `json:"refusal"`
}

func TestSaveAnswersWhatThePythonTierAnswered(t *testing.T) {
	raw, err := os.ReadFile("testdata/documents.json")
	if err != nil {
		t.Fatalf("read the capture: %v", err)
	}
	var captures []capture
	if err := json.Unmarshal(raw, &captures); err != nil {
		t.Fatalf("decode the capture: %v", err)
	}
	if len(captures) == 0 {
		t.Fatal("the capture is empty")
	}

	for _, want := range captures {
		got := Save(want.Sent, want.Stored)
		if got.Refusal != want.Refusal {
			t.Errorf("%s: refusal %q, the Python tier answered %q",
				want.Name, got.Refusal, want.Refusal)
			continue
		}
		if got.Answer != want.Answered {
			t.Errorf("%s: answered %q, the Python tier answered %q",
				want.Name, got.Answer, want.Answered)
		}
		if got.Write && got.Store != want.Answered {
			t.Errorf("%s: stored %q but answered %q; the two are one document there",
				want.Name, got.Store, got.Answer)
		}
	}
}

// The four documents long enough to be refused for their length are generated
// rather than captured: writing 100 kB of "x" into testdata to prove one
// comparison is a worse fixture than the arithmetic. The messages and the
// counts are the Python tier's, asked for the same four documents on the same
// run as the capture beside this test.
func TestSaveRefusesALongDocumentInThePythonTiersWords(t *testing.T) {
	const prefix = "## H\n"
	cases := []struct {
		name    string
		sent    string
		refusal string
	}{
		{
			name: "exactly the hard limit is accepted",
			sent: prefix + repeat("x", HardLimit-len(prefix)),
		},
		{
			name:    "one character over",
			sent:    prefix + repeat("x", HardLimit-len(prefix)+1),
			refusal: "Memory exceeds 25,000 character limit (25,001 chars). Consolidate by merging related items, removing outdated entries, and shortening descriptions.",
		},
		{
			name: "over the limit in astral characters, which are one character each",
			sent: prefix + repeat("\U0001f600", HardLimit),
			refusal: "Memory exceeds 25,000 character limit (25,005 chars). Consolidate by merging related items, " +
				"removing outdated entries, and shortening descriptions.",
		},
	}
	for _, tc := range cases {
		got := Save(tc.sent, "")
		if got.Refusal != tc.refusal {
			t.Errorf("%s: refusal %q, want %q", tc.name, got.Refusal, tc.refusal)
		}
		if tc.refusal == "" && !got.Write {
			t.Errorf("%s: nothing was written", tc.name)
		}
	}
}

func repeat(text string, count int) string {
	built := make([]byte, 0, len(text)*count)
	for range count {
		built = append(built, text...)
	}
	return string(built)
}
