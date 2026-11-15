package cite

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
)

func TestLabelledSourcesRoundTrip(t *testing.T) {
	for _, c := range []struct {
		name   string
		source citation.Source
		kind   string
		label  string
	}{
		{"mail", MailSource("Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-03"), Mail, "Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-03"},
		{"app", AppSource("GitHub · search_issues"), App, "GitHub · search_issues"},
		{"a subject that would close the marker", MailSource("Re: [urgent]\n] done"), Mail, "Re: [urgent] ] done"},
		{"empty is said, not blank", AppSource("  \n "), App, "Untitled"},
	} {
		kind, label, ok := LabelOf(c.source.Ref)
		if !ok || kind != c.kind || label != c.label || c.source.Kind != c.kind {
			t.Errorf("%s: got %q %q %v from %+v", c.name, kind, label, ok, c.source)
		}
		// The marker must be one the apps can match: no bracket, no space,
		// no line break, and only the base64url alphabet after the prefix.
		if strings.ContainsAny(c.source.Ref, "[] \n=+/") {
			t.Errorf("%s: the ref %q cannot sit in a marker", c.name, c.source.Ref)
		}
	}
}

func TestALongLabelIsCutOnACharacter(t *testing.T) {
	_, label, ok := LabelOf(MailSource(strings.Repeat("é", 300)).Ref)
	if !ok || len(label) > MaxLabelBytes+len("…") || !strings.HasSuffix(label, "…") {
		t.Fatalf("got %d bytes, ok=%v: %q", len(label), ok, label)
	}
}

func TestLabelOfRefusesOtherRefs(t *testing.T) {
	for _, ref := range []string{"481", "https://example.org", "src_mail_***", "src_other_QQ", "src_app_" + "_w"} {
		if _, _, ok := LabelOf(ref); ok {
			t.Errorf("%q was read as a labelled ref", ref)
		}
	}
}

func TestRewriteWritesTheLabelledMarker(t *testing.T) {
	reg := citation.NewRegistry()
	n := reg.Number(MailSource("Ann · Invoice"))
	got := Rewrite("It is due Friday [1].", reg.Lookup)
	if n != 1 || got != "It is due Friday [citation:"+MailSource("Ann · Invoice").Ref+"]." {
		t.Fatalf("got %q", got)
	}
}
