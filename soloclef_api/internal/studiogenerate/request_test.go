package studiogenerate

import (
	"strings"
	"testing"
)

func TestADialogsRequestIsReadWholeOrRefused(t *testing.T) {
	accepted := []struct {
		name, body string
		check      func(t *testing.T, r Request)
	}{
		{"a bare kind takes every default", `{"kind":"quiz"}`, func(t *testing.T, r Request) {
			if r.Options["count"] != "standard" || r.Options["difficulty"] != "medium" || r.SourceIDs != nil {
				t.Errorf("%+v", r)
			}
		}},
		{"presets, focus trimmed, sources named", `{"kind":"audio","format":"debate","length":"long","focus":"  the trial  ","source_ids":[3,4],"request_id":"abc_1-2","title":" T ","language":"fr"}`, func(t *testing.T, r Request) {
			if r.Options["format"] != "debate" || r.Options["length"] != "long" || r.Focus != "the trial" || len(r.SourceIDs) != 2 || r.RequestID != "abc_1-2" || r.Title != "T" || r.Language != "fr" {
				t.Errorf("%+v", r)
			}
		}},
		{"an empty source list is kept as empty, not as absent", `{"kind":"mindmap","source_ids":[]}`, func(t *testing.T, r Request) {
			if r.SourceIDs == nil || len(r.SourceIDs) != 0 {
				t.Errorf("%#v", r.SourceIDs)
			}
		}},
		{"a custom video style keeps its description on one line", `{"kind":"video","visual_style":"custom","style_prompt":"  neon \n city "}`, func(t *testing.T, r Request) {
			if r.Options["style_prompt"] != "neon city" {
				t.Errorf("%q", r.Options["style_prompt"])
			}
		}},
		{"a style description without the custom style is dropped", `{"kind":"video","visual_style":"dark","style_prompt":"neon"}`, func(t *testing.T, r Request) {
			if _, kept := r.Options["style_prompt"]; kept {
				t.Error("kept")
			}
		}},
		{"an interactive report has no file type", `{"kind":"report","format":"interactive","file_type":"docx","template_prompt":" Brief it "}`, func(t *testing.T, r Request) {
			if _, kept := r.Options["file_type"]; kept || r.Options["template_prompt"] != "Brief it" {
				t.Errorf("%+v", r.Options)
			}
		}},
		{"an infographic style from the specialist's own list", `{"kind":"infographic","style":"scientific","orientation":"portrait","detail":"concise"}`, func(t *testing.T, r Request) {
			if r.Options["style"] != "scientific" {
				t.Errorf("%+v", r.Options)
			}
		}},
	}
	for _, c := range accepted {
		request, failures := parseRequest([]byte(c.body))
		if len(failures) > 0 {
			t.Errorf("%s: refused: %+v", c.name, failures)
			continue
		}
		c.check(t, request)
	}

	refused := []struct{ name, body, field string }{
		{"not an object", `[1]`, "body"},
		{"no kind", `{}`, "kind"},
		{"an unknown kind", `{"kind":"hologram"}`, "kind"},
		{"a preset outside its vocabulary", `{"kind":"quiz","count":"hundreds"}`, "count"},
		{"a field the kind does not have", `{"kind":"mindmap","difficulty":"hard"}`, "difficulty"},
		{"a field no kind has", `{"kind":"quiz","user_id":"x"}`, "user_id"},
		{"a custom style with no description", `{"kind":"video","visual_style":"custom","style_prompt":"  "}`, "style_prompt"},
		{"a request id with other characters", `{"kind":"quiz","request_id":"a b"}`, "request_id"},
		{"a focus past its bound", `{"kind":"quiz","focus":"` + strings.Repeat("x", 2001) + `"}`, "focus"},
		{"sources that are not numbers", `{"kind":"quiz","source_ids":["a"]}`, "source_ids"},
		{"an infographic style that does not exist", `{"kind":"infographic","style":"oil"}`, "style"},
	}
	for _, c := range refused {
		_, failures := parseRequest([]byte(c.body))
		if len(failures) == 0 {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		named := false
		for _, failure := range failures {
			named = named || failure.Location[len(failure.Location)-1] == c.field
		}
		if !named {
			t.Errorf("%s: the refusal does not name %s: %+v", c.name, c.field, failures)
		}
	}

	// Every problem is reported at once.
	if _, failures := parseRequest([]byte(`{"kind":"quiz","count":"x","difficulty":"y","extra":1}`)); len(failures) != 3 {
		t.Errorf("three problems, %d reported: %+v", len(failures), failures)
	}
}

func TestTheStoredOptionsAreWhatThePanelReadsBack(t *testing.T) {
	request, _ := parseRequest([]byte(`{"kind":"slides","length":"short","focus":"costs","title":"Deck","request_id":"r1","source_ids":[1]}`))
	stored, err := request.Stored()
	if err != nil {
		t.Fatal(err)
	}
	got := string(stored)
	for _, piece := range []string{`"kind":"slides"`, `"length":"short"`, `"format":"detailed"`, `"focus":"costs"`} {
		if !strings.Contains(got, piece) {
			t.Errorf("%s lacks %s", got, piece)
		}
	}
	for _, absent := range []string{"title", "request_id", "source_ids"} {
		if strings.Contains(got, absent) {
			t.Errorf("%s keeps %s, which is not an option", got, absent)
		}
	}
}
