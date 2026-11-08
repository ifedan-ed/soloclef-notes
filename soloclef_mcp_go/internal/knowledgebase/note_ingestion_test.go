package knowledgebase

import (
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
)

// The envelope's member order and spelling are the browser extension's, because
// the create endpoint reads that shape; a note added here must be indistinguishable
// from a saved page.
func TestTheNoteEnvelopeIsTheExtensionShape(t *testing.T) {
	moment := time.Date(2026, 11, 8, 5, 22, 43, 478894000, time.UTC)
	envelope := buildNoteDocument(1, "Synthetic note", "- a\n- b", "", moment)
	const want = `{"document_type":"EXTENSION","workspace_id":1,"content":[{"metadata":` +
		`{"BrowsingSessionId":"surfsense-mcp",` +
		`"VisitedWebPageURL":"https://surfsense.local/mcp-note/synthetic-note-20261108052243",` +
		`"VisitedWebPageTitle":"Synthetic note",` +
		`"VisitedWebPageDateWithTimeInISOString":"2026-11-08T05:22:43.478894+00:00",` +
		`"VisitedWebPageReffererURL":"",` +
		`"VisitedWebPageVisitDurationInMilliseconds":"0"},` +
		`"pageContent":"- a\n- b"}]}`
	if got := pythonjson.Compact(envelope); got != want {
		t.Errorf("got\n %s\nwant\n %s", got, want)
	}
}

// A source URL, when the caller gave one, stands in for the synthetic link.
func TestAGivenSourceURLIsUsedAsThePageURL(t *testing.T) {
	envelope := buildNoteDocument(1, "Title", "x", "https://example.invalid/p",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	rendered := pythonjson.Compact(envelope)
	if !strings.Contains(rendered, `"VisitedWebPageURL":"https://example.invalid/p"`) {
		t.Errorf("the source URL was not used: %s", rendered)
	}
	if strings.Contains(rendered, "mcp-note") {
		t.Errorf("a synthetic link was used as well: %s", rendered)
	}
}

// The synthetic link is derived from the title, so the same note saved twice
// does not collide and a person can recognise it in a list.
func TestTheSyntheticLinkIsASlugOfTheTitle(t *testing.T) {
	moment := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := map[string]string{
		"Weird / Title!":   "weird-title",
		"  Spaces  Here ":  "spaces-here",
		"UPPER and Lower":  "upper-and-lower",
		"---":              "note",
		"":                 "note",
		"ümlaut":           "mlaut",
		"two__underscores": "two-underscores",
	}
	for title, slug := range cases {
		got := syntheticURL(title, moment)
		want := "https://surfsense.local/mcp-note/" + slug + "-20260102030405"
		if got != want {
			t.Errorf("%q became %s, want %s", title, got, want)
		}
	}
}

// datetime.isoformat() prints microseconds only when there are any, and writes
// the offset as "+00:00" rather than "Z".
func TestTheCaptureTimeIsPythonsISOFormat(t *testing.T) {
	cases := map[string]time.Time{
		"2026-11-08T05:22:43.478894+00:00": time.Date(2026, 11, 8, 5, 22, 43, 478894000, time.UTC),
		"2026-11-08T05:22:43+00:00":        time.Date(2026, 11, 8, 5, 22, 43, 0, time.UTC),
		"2026-11-08T05:22:43.000001+00:00": time.Date(2026, 11, 8, 5, 22, 43, 1000, time.UTC),
	}
	for want, moment := range cases {
		if got := isoFormat(moment); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

// A time in another zone is converted, not relabelled, because the envelope
// says the capture was UTC.
func TestANonUTCTimeIsConverted(t *testing.T) {
	zone := time.FixedZone("plus-two", 2*60*60)
	moment := time.Date(2026, 11, 8, 7, 22, 43, 0, zone)
	if got := isoFormat(moment); got != "2026-11-08T05:22:43+00:00" {
		t.Errorf("got %s", got)
	}
}
