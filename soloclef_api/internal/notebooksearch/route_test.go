package notebooksearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
)

// fixedSearch answers the same documents to every search and keeps what it
// was asked.
type fixedSearch struct {
	hits   []documents.Hit
	err    error
	users  []string
	scopes []documents.Scope
}

func (f *fixedSearch) SearchScoped(_ context.Context, userID, _ string, scope documents.Scope, _ int) ([]documents.Hit, documents.Outcome, error) {
	f.users = append(f.users, userID)
	f.scopes = append(f.scopes, scope)
	if f.err != nil {
		return nil, documents.Searched, f.err
	}
	// As the real search does: nothing outside the limit comes back.
	var within []documents.Hit
	for _, hit := range f.hits {
		for _, id := range scope.Limit {
			if id == hit.DocumentID {
				within = append(within, hit)
			}
		}
	}
	return within, documents.Searched, nil
}

// fixedRows answers one account's rows and nothing to anyone else.
type fixedRows struct {
	owner string
	rows  []NotebookDocumentsRow
	err   error
}

func (f fixedRows) NotebookDocuments(_ context.Context, userID string) ([]NotebookDocumentsRow, error) {
	if f.err != nil || userID != f.owner {
		return nil, f.err
	}
	return f.rows, nil
}

func search(t *testing.T, route Route, userID, target string) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Errors, route.Logger = errorenvelope.Writer{}, slog.New(slog.NewTextHandler(io.Discard, nil))
	route.Register(api)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request = request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// near sets how near the vector leg found each of a hit's passages, in
// order; a passage it is not given stays at 0, found by its words alone.
func near(h documents.Hit, similarities ...float64) documents.Hit {
	for i, similarity := range similarities {
		h.Chunks[i].Similarity = similarity
	}
	return h
}

func hit(id int64, title string, passages ...string) documents.Hit {
	h := documents.Hit{DocumentID: id, Title: title, DocumentType: "FILE"}
	for i, text := range passages {
		h.Chunks = append(h.Chunks, documents.Chunk{ID: id*100 + int64(i), Content: text, Position: int64(i), DocumentID: id, Title: title})
	}
	return h
}

func TestTheRouteGroupsPassagesUnderTheirNotebooks(t *testing.T) {
	folder := "Papers"
	updated := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	card := func(notebook int64, title string) NotebookDocumentsRow {
		return NotebookDocumentsRow{NotebookID: notebook, Title: title, UpdatedAt: updated}
	}
	row := func(base NotebookDocumentsRow, document, source int64, reach int32, folderName *string) NotebookDocumentsRow {
		base.DocumentID, base.SourceID, base.Reach, base.FolderName = document, source, reach, folderName
		return base
	}
	lungs, heart := card(1, "Lungs"), card(2, "Heart")
	rows := fixedRows{owner: "alice", rows: []NotebookDocumentsRow{
		row(lungs, 10, 501, reachSource, nil), row(lungs, 11, 0, reachNote, nil), row(lungs, 12, 502, reachFolder, &folder),
		row(heart, 10, 601, reachSource, nil), row(heart, 13, 602, reachSource, nil),
	}}
	found := &fixedSearch{hits: []documents.Hit{
		near(hit(10, "Guideline", "Intro with no match.", "Use a **spacer** every time."), 0.71, 0.8),
		hit(11, "My note", "Remember the spacer."),
		near(hit(12, "Paper", "Nothing about the query here."), 0.62),
		near(hit(13, "Far", "Unrelated passage, found by nearness alone."), 0.9),
		hit(99, "Not in any notebook", "spacer spacer"),
	}}
	status, body := search(t, Route{Notebooks: rows, Search: found}, "alice", "/api/v1/notebooks/search?q=+spacer+")
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	var got struct {
		Query     string
		Notebooks []struct {
			ID        int64
			Title     string
			UpdatedAt string `json:"updated_at"`
			Passages  []passage
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Query != "spacer" || len(got.Notebooks) != 2 || got.Notebooks[0].Title != "Lungs" || got.Notebooks[1].Title != "Heart" ||
		got.Notebooks[0].UpdatedAt != "2026-11-01T08:00:00Z" {
		t.Fatalf("the notebooks: %s", body)
	}
	type shape struct {
		kind, title, snippet   string
		document, note, source int64
		folder                 string
		highlighted            []string
	}
	shapes := func(passages []passage) (out []shape) {
		deref := func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		}
		for _, p := range passages {
			s := shape{kind: p.Kind, title: p.Title, snippet: p.Snippet, document: deref(p.DocumentID), note: deref(p.NoteID), source: deref(p.SourceID)}
			if p.FolderName != nil {
				s.folder = *p.FolderName
			}
			s.highlighted = sliced(p.Snippet, p.Highlights)
			out = append(out, s)
		}
		return out
	}
	// Lungs: the guideline's matching passage first (one per document in
	// the first pass), then the note, then the folder's paper, which has no
	// match but is close and among the three best documents. The guideline's other
	// passage finds no slot left.
	wantLungs := []shape{
		{kind: "source", title: "Guideline", snippet: "Use a spacer every time.", document: 10, source: 501, highlighted: []string{"spacer"}},
		{kind: "note", title: "My note", snippet: "Remember the spacer.", note: 11, highlighted: []string{"spacer"}},
		{kind: "source", title: "Paper", snippet: "Nothing about the query here.", document: 12, source: 502, folder: "Papers", highlighted: []string{}},
	}
	if got := shapes(got.Notebooks[0].Passages); !reflect.DeepEqual(got, wantLungs) {
		t.Errorf("Lungs:\n got %+v\nwant %+v", got, wantLungs)
	}
	// Heart: the same guideline under its own source row, both passages;
	// the fourth document's passage holds no word of the query and is past
	// the three best, so it is not shown however close it is.
	wantHeart := []shape{
		{kind: "source", title: "Guideline", snippet: "Use a spacer every time.", document: 10, source: 601, highlighted: []string{"spacer"}},
		{kind: "source", title: "Guideline", snippet: "Intro with no match.", document: 10, source: 601, highlighted: []string{}},
	}
	if got := shapes(got.Notebooks[1].Passages); !reflect.DeepEqual(got, wantHeart) {
		t.Errorf("Heart:\n got %+v\nwant %+v", got, wantHeart)
	}
	if !strings.Contains(body, `"note_id":null`) || !strings.Contains(body, `"folder_name":null`) || !strings.Contains(body, `"took_ms":`) {
		t.Errorf("the shape the clients read is missing a member: %s", body)
	}
	if strings.Contains(body, "Not in any notebook") {
		t.Error("a document outside every notebook was shown")
	}
	scope := found.scopes[0]
	if !scope.Limited || !reflect.DeepEqual(scope.Limit, []int64{10, 11, 12, 13}) || found.users[0] != "alice" {
		t.Errorf("searched as %q within %+v, want her notebooks' documents as a hard limit", found.users[0], scope)
	}
}

// A passage with none of the query's words is shown only when the vector
// leg found it close. The leg has nearest neighbours for any query, so
// without the line a search for gibberish would fill the box, and the page
// could never say that nothing inside the notebooks matches.
func TestAPassageFoundByMeaningAloneMustBeClose(t *testing.T) {
	terms := queryTerms("zzqqxx nothing")
	got := ranked([]documents.Hit{
		near(hit(1, "Paraphrase", "Said in other words."), 0.74),
		near(hit(2, "Neighbour", "Merely the nearest thing there is."), 0.41),
		hit(3, "Keyword", "Nothing else, but the word is here."),
	}, terms)
	var titles []string
	for _, c := range got {
		titles = append(titles, c.title)
	}
	if !reflect.DeepEqual(titles, []string{"Paraphrase", "Keyword"}) {
		t.Fatalf("shown = %v, want the close paraphrase and the passage with a word of the query, and not the far neighbour", titles)
	}
}

func TestTheRouteRefusesAndAnswersEmpty(t *testing.T) {
	rows := fixedRows{owner: "alice", rows: []NotebookDocumentsRow{{NotebookID: 1, DocumentID: 10, SourceID: 5, Reach: reachSource, Title: "Lungs"}}}
	hits := []documents.Hit{hit(10, "Guideline", "Use a spacer.")}
	cases := []struct {
		name     string
		user     string
		target   string
		rows     Notebooks
		search   fixedSearch
		status   int
		says     string
		searched bool
	}{
		{name: "a match", target: "?q=spacer&limit=1", status: 200, says: `"title":"Lungs"`, searched: true},
		{name: "one character", target: "?q=s", status: 200, says: `"notebooks":[]`},
		{name: "no query", target: "", status: 200, says: `"query":"","notebooks":[]`},
		{name: "an account with no notebooks", user: "bob", target: "?q=spacer", status: 200, says: `"notebooks":[]`},
		{name: "a query past the limit", target: "?q=" + strings.Repeat("a", maxQueryChars+1), status: 422, says: "q"},
		{name: "a limit of nought", target: "?q=spacer&limit=0", status: 422, says: "limit"},
		{name: "a limit past the cap", target: "?q=spacer&limit=9", status: 422, says: "limit"},
		{name: "a limit that is not a number", target: "?q=spacer&limit=many", status: 422, says: "limit"},
		{name: "a search that cannot run", target: "?q=spacer", search: fixedSearch{err: errors.New("qdrant: connection refused")},
			status: 503, says: "Search is not available", searched: true},
		{name: "a database failure", target: "?q=spacer", rows: fixedRows{err: errors.New("down")}, status: 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			user, store := c.user, c.rows
			if user == "" {
				user = "alice"
			}
			if store == nil {
				store = rows
			}
			found := c.search
			found.hits = hits
			status, body := search(t, Route{Notebooks: store, Search: &found}, user, "/api/v1/notebooks/search"+c.target)
			if status != c.status || !strings.Contains(body, c.says) {
				t.Fatalf("got %d %s, want %d saying %q", status, body, c.status, c.says)
			}
			if searched := len(found.scopes) > 0; searched != c.searched {
				t.Errorf("searched: %v, want %v", searched, c.searched)
			}
			if strings.Contains(body, "qdrant") {
				t.Errorf("the refusal names the store: %s", body)
			}
		})
	}
}

func TestTheCapsHoldAcrossNotebooks(t *testing.T) {
	// Twelve notebooks each reading its own document, every one matching,
	// and a thirteenth document read by all of them.
	links := map[int64][]link{}
	var ranking []candidate
	for n := int64(1); n <= 12; n++ {
		links[n] = []link{{notebookID: n, sourceID: n, reach: reachSource}}
		links[100] = append(links[100], link{notebookID: n, sourceID: 100 + n, reach: reachSource})
		for range 4 {
			ranking = append(ranking, candidate{documentID: n, plain: "p"})
		}
	}
	ranking = append(ranking, candidate{documentID: 100, plain: "shared"})
	groups := groupPassages(ranking, links, maxNotebooks, maxPerNotebook, maxPassages)
	if len(groups) != maxNotebooks {
		t.Fatalf("%d notebooks, want %d", len(groups), maxNotebooks)
	}
	total := 0
	for i, g := range groups {
		if g.notebookID != int64(i+1) {
			t.Errorf("group %d is notebook %d: groups are in order of their best passage", i, g.notebookID)
		}
		if len(g.passages) > maxPerNotebook {
			t.Errorf("notebook %d has %d passages", g.notebookID, len(g.passages))
		}
		for j := 1; j < len(g.passages); j++ {
			if g.passages[j-1].rank > g.passages[j].rank {
				t.Errorf("notebook %d's passages are out of rank order", g.notebookID)
			}
		}
		total += len(g.passages)
	}
	if total != maxPassages {
		t.Errorf("%d passages in all, want the cap of %d", total, maxPassages)
	}
	// Variety first: each notebook shows the shared document before a
	// second passage of its own.
	if first := groups[0].passages; len(first) < 2 || first[1].candidate.documentID == first[0].candidate.documentID && first[len(first)-1].candidate.documentID != 100 {
		t.Errorf("notebook 1 shows %+v, want its own document and the shared one before a repeat", first)
	}
}
