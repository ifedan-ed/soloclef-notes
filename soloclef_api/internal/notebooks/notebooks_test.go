package notebooks

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsDefaultTitle(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Untitled notebook", true},
		{"  UNTITLED \t notebook\n", true},
		{"Cuaderno sin título", true},
		{"CUADERNO SIN TÍTULO", true},
		{"Caderno sem título", true},
		{"बिना शीर्षक की नोटबुक", true},
		{"제목   없는 노트북", true},
		{"未命名笔记本", true},
		{"", false},
		{"Untitled", false},
		{"Untitled notebooks", false},
		{"Untitlednotebook", false},
		{"Tide tables", false},
	}
	for _, c := range cases {
		if got := isDefaultTitle(c.title); got != c.want {
			t.Errorf("isDefaultTitle(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

func TestStillNaming(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	twoMinutesAgo := now.Add(-recentlyReady)
	cases := []struct {
		name      string
		source    string
		state     string // "" is SQL NULL
		candidate bool
		since     time.Time
	}{
		{"named by its user", "user", "", false, time.Time{}},
		{"never named", "auto", "", true, twoMinutesAgo},
		{"state is JSON null", "auto", "null", true, twoMinutesAgo},
		{"state is a list", "auto", "[1, 2]", true, twoMinutesAgo},
		{"state is not JSON", "auto", "{", true, twoMinutesAgo},
		{"runs zero", "auto", `{"runs": 0}`, true, twoMinutesAgo},
		{"runs is not a count", "auto", `{"runs": "many"}`, true, twoMinutesAgo},
		{"runs negative", "auto", `{"runs": -1}`, true, twoMinutesAgo},
		{"named once ten minutes ago", "auto", `{"runs": 1, "at": "2026-06-15T11:50:00+00:00"}`, true, twoMinutesAgo},
		{"named once thirty seconds ago", "auto", `{"runs": 1, "at": "2026-06-15T11:59:30+00:00"}`, true, now.Add(-30 * time.Second)},
		{"named once, at the window's edge", "auto", `{"runs": 1, "at": "2026-06-15T11:30:00Z"}`, true, twoMinutesAgo},
		{"named once, past the window", "auto", `{"runs": 1, "at": "2026-06-15T11:29:59+00:00"}`, false, time.Time{}},
		{"named once, no time", "auto", `{"runs": 1}`, false, time.Time{}},
		{"named once, unreadable time", "auto", `{"runs": 1, "at": "yesterday-ish"}`, false, time.Time{}},
		{"named once, time with no offset", "auto", `{"runs": 1, "at": "2026-06-15T11:50:00"}`, false, time.Time{}},
		{"never named, unreadable time", "auto", `{"runs": 0, "at": 5}`, true, twoMinutesAgo},
		{"named twice", "auto", `{"runs": 2, "at": "2026-06-15T11:59:00+00:00"}`, false, time.Time{}},
		{"an enormous count", "auto", `{"runs": 99999999999999999999999}`, false, time.Time{}},
	}
	for _, c := range cases {
		notebook := Notebook{ID: 7, TitleSource: c.source}
		if c.state != "" {
			notebook.AutoTitle = []byte(c.state)
		}
		got, may := stillNaming(notebook, now)
		if may != c.candidate {
			t.Errorf("%s: candidate = %v, want %v", c.name, may, c.candidate)
			continue
		}
		if may && (!got.readySince.Equal(c.since) || got.notebookID != 7) {
			t.Errorf("%s: ready since %v, want %v", c.name, got.readySince, c.since)
		}
	}
}

func TestStoredEdit(t *testing.T) {
	seed := seedFor("blog_post")
	text := func(s string) *string { return &s }
	cases := []struct {
		name                       string
		edit                       templateEdit
		title, description, prompt string
	}{
		{"prompt only", templateEdit{Prompt: " p "}, seed.title, seed.description, "p"},
		{"all three", templateEdit{Title: text(" T "), Description: text(" D "), Prompt: "P"}, "T", "D", "P"},
		{"blank title is the seed's", templateEdit{Title: text("  "), Prompt: "P"}, seed.title, seed.description, "P"},
		{"blank description is emptied", templateEdit{Description: text("  "), Prompt: "P"}, seed.title, "", "P"},
		{"blank prompt is the seed's", templateEdit{Prompt: " \n "}, seed.title, seed.description, seed.prompt},
	}
	for _, c := range cases {
		title, description, prompt := storedEdit(seed, c.edit)
		if title != c.title || description != c.description || prompt != c.prompt {
			t.Errorf("%s: stored (%q, %q, %q), want (%q, %q, %q)", c.name,
				title, description, prompt, c.title, c.description, c.prompt)
		}
	}
	if seedFor("BLOG_POST") != nil || seedFor("no_such_template") != nil {
		t.Error("a key that is not seeded named a template")
	}
}

// failuresOf is the "field: message" of each failure, in order.
func failuresOf(read func(string, []byte) ([]string, error), body string) []string {
	failures, _ := read("application/json", []byte(body))
	return failures
}

func TestBodies(t *testing.T) {
	long := func(n int) string { return strings.Repeat("é", n) }
	flat := func(failures []struct{ loc, msg string }) []string {
		out := []string{}
		for _, f := range failures {
			out = append(out, f.loc+": "+f.msg)
		}
		return out
	}
	readers := map[string]func(string, []byte) ([]string, error){
		"notebook": func(ct string, b []byte) ([]string, error) {
			_, f, err := readNotebookCreation(ct, b)
			var fs []struct{ loc, msg string }
			for _, x := range f {
				fs = append(fs, struct{ loc, msg string }{strings.Join(x.Location, "."), x.Message})
			}
			return flat(fs), err
		},
		"template": func(ct string, b []byte) ([]string, error) {
			_, f, err := readTemplateEdit(ct, b)
			var fs []struct{ loc, msg string }
			for _, x := range f {
				fs = append(fs, struct{ loc, msg string }{strings.Join(x.Location, "."), x.Message})
			}
			return flat(fs), err
		},
	}
	cases := []struct {
		reader, body string
		want         []string
	}{
		{"notebook", `{}`, nil},
		{"notebook", `{"title": "x", "icon": "📓", "extra": 1}`, nil},
		{"notebook", `{"title": "` + long(200) + `"}`, nil},
		{"notebook", `{"title": "` + long(201) + `"}`, []string{"body.title: String should have at most 200 characters"}},
		{"notebook", `{"title": 1, "icon": 2}`, []string{"body.title: Input should be a valid string", "body.icon: Input should be a valid string"}},
		{"notebook", `{"icon": "` + long(17) + `"}`, []string{"body.icon: String should have at most 16 characters"}},
		{"notebook", `{"title": "a\u0000b"}`, []string{"body.title: " + nulRefused}},
		{"notebook", `[]`, []string{"body: Input should be a valid dictionary or object to extract fields from"}},
		{"template", `{"prompt": "p"}`, nil},
		{"template", `{}`, []string{"body.prompt: Field required"}},
		{"template", `{"prompt": ""}`, []string{"body.prompt: String should have at least 1 character"}},
		{"template", `{"description": "` + long(501) + `", "prompt": "p"}`, []string{"body.description: String should have at most 500 characters"}},
		{"template", `{"title": "a\u0000", "prompt": "b\u0000"}`, []string{"body.title: " + nulRefused, "body.prompt: " + nulRefused}},
	}
	for _, c := range cases {
		got := failuresOf(readers[c.reader], c.body)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s %s: failures %q, want %q", c.reader, c.body, got, c.want)
		}
	}

	// What a valid body is read as.
	created, _, _ := readNotebookCreation("application/json", []byte(`{"title": "  Tides \n"}`))
	if created.Title != "Tides" {
		t.Errorf("a notebook's title is stripped: got %q", created.Title)
	}
	if blank, _, _ := readNotebookCreation("application/json", []byte(`{"title": "   "}`)); blank.Title != defaultTitle {
		t.Errorf("a blank title is the default: got %q", blank.Title)
	}
}

// TestPatternsRegisterTogether: ServeMux panics on two patterns where
// neither is more specific, which would stop the process starting. The
// search across notebooks is registered beside them (internal/notebooksearch)
// and is a literal, which ServeMux prefers to `{notebook_id}`.
func TestPatternsRegisterTogether(t *testing.T) {
	mux := http.NewServeMux()
	for _, pattern := range append(Served(), http.MethodGet+" /api/v1/notebooks/search") {
		mux.Handle(pattern, http.NotFoundHandler())
	}
	if len(Served()) != 10 {
		t.Errorf("%d routes listed, want 10", len(Served()))
	}
}
