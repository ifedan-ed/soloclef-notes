package knowledgebase

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
)

// The create route refuses a field it does not know, so the body is exactly
// these three members.
func TestANoteIsSentAsTheCreateRouteReadsIt(t *testing.T) {
	for _, c := range []struct {
		name, source string
		given        bool
		want         string
	}{
		{"with no source, none is invented", "", false,
			`{"title":"Synthetic note","content":"- a\n- b","source_url":null}`},
		{"an empty source is no source", "", true,
			`{"title":"Synthetic note","content":"- a\n- b","source_url":null}`},
		{"a given source is sent as it is", "https://example.invalid/p", true,
			`{"title":"Synthetic note","content":"- a\n- b","source_url":"https://example.invalid/p"}`},
	} {
		if got := pythonjson.Compact(noteBody("Synthetic note", "- a\n- b", c.source, c.given)); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}
