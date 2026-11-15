package markdownmemory

import (
	"strings"
	"testing"
)

func TestAPersonsSaveKeepsAnyTextAndFilesLooseTextUnderNotes(t *testing.T) {
	const stored = "## Preferences\n- 2026-01-02: prefers short answers"
	cases := []struct {
		name string
		sent string
		want string
	}{
		{
			name: "plain text alone",
			sent: "I teach chemistry",
			want: "## Notes\nI teach chemistry",
		},
		{
			name: "several plain lines, blank lines inside kept",
			sent: "  I teach chemistry\n\nSecond line  \n",
			want: "## Notes\nI teach chemistry\n\nSecond line",
		},
		{
			name: "text before the first heading opens Notes at the top",
			sent: "I teach chemistry\n## Facts\n- 2026-01-01: lives in Lagos",
			want: "## Notes\nI teach chemistry\n\n## Facts\n- 2026-01-01: lives in Lagos",
		},
		{
			name: "text before the first heading joins an existing Notes section, ahead of its lines",
			sent: "I teach chemistry\n## Facts\n- 2026-01-01: lives in Lagos\n## Notes\n- 2026-01-02: older note",
			want: "## Facts\n- 2026-01-01: lives in Lagos\n\n## Notes\nI teach chemistry\n- 2026-01-02: older note",
		},
		{
			name: "the existing Notes heading is matched in any case and keeps its own spelling",
			sent: "loose\n## notes\nkept",
			want: "## notes\nloose\nkept",
		},
		{
			name: "only the first of two Notes sections takes the loose text",
			sent: "loose\n## Notes\none\n## Notes\ntwo",
			want: "## Notes\nloose\none\n\n## Notes\ntwo",
		},
		{
			name: "a sectioned document is kept as written, plain lines included",
			sent: "## Facts\nplain line\n- 2026-01-01: dated",
			want: "## Facts\nplain line\n- 2026-01-01: dated",
		},
		{
			name: "a second save of the answer changes nothing",
			sent: "## Notes\nI teach chemistry\n\n## Facts\n- 2026-01-01: lives in Lagos",
			want: "## Notes\nI teach chemistry\n\n## Facts\n- 2026-01-01: lives in Lagos",
		},
		{
			name: "loose bullets are read as bullets under Notes, a legacy one in canonical form",
			sent: "- (2026-01-01) [fact] legacy\n- 2026-01-02: dated",
			want: "## Notes\n- 2026-01-01: legacy\n- 2026-01-02: dated",
		},
		{
			name: "a line that only looks like a heading is loose text",
			sent: "##H\n- 2026-01-01: x",
			want: "## Notes\n##H\n- 2026-01-01: x",
		},
		{
			name: "the assistant's no-update words are a person's ordinary text",
			sent: "No change",
			want: "## Notes\nNo change",
		},
	}
	for _, tc := range cases {
		got := SaveWrittenByPerson(tc.sent, stored)
		if got.Refusal != "" || !got.Write {
			t.Errorf("%s: not written (refusal %q)", tc.name, got.Refusal)
			continue
		}
		if got.Store != tc.want || got.Answer != tc.want {
			t.Errorf("%s: stored %q answered %q, want %q", tc.name, got.Store, got.Answer, tc.want)
		}
	}
}

func TestAPersonsEmptySaveClearsTheMemory(t *testing.T) {
	for _, sent := range []string{"", "   \n\t  ", "\x1c\x1d\x1e"} {
		got := SaveWrittenByPerson(sent, "## Facts\n- 2026-01-01: x")
		if !got.Write || got.Store != "" || got.Answer != "" || got.Refusal != "" {
			t.Errorf("%q: %+v, want a clear", sent, got)
		}
	}
}

// The limit is measured on the stored document, Notes heading included,
// because the chat's prompt shows at most HardLimit characters of it.
func TestAPersonsSaveIsHeldToTheLimitOfWhatIsStored(t *testing.T) {
	const stored = "## Facts\n- 2026-01-01: x"
	heading := len("## " + notesHeading + "\n")

	fits := SaveWrittenByPerson(strings.Repeat("x", HardLimit-heading), stored)
	if !fits.Write || len(fits.Store) != HardLimit {
		t.Errorf("a document exactly at the limit once filed: written %v, %d characters", fits.Write, len(fits.Store))
	}

	over := SaveWrittenByPerson(strings.Repeat("x", HardLimit), stored)
	const refusal = "Memory exceeds 25,000 character limit (25,009 chars). Consolidate by merging related items, " +
		"removing outdated entries, and shortening descriptions."
	if over.Write || over.Refusal != refusal || over.Answer != stored {
		t.Errorf("a document over the limit once filed: %+v", over)
	}
}

// The assistant's save is unchanged: the chat's tool is still told to write
// sections (TestSaveAnswersWhatThePythonTierAnswered holds the rest of it).
func TestTheAssistantsSaveStillRefusesPlainText(t *testing.T) {
	got := Save("I teach chemistry", "")
	if got.Write || got.Refusal != "Memory must be markdown with at least one ## heading." {
		t.Errorf("the assistant's plain-text save: %+v", got)
	}
}
