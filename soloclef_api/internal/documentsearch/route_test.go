package documentsearch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_DOCUMENTSEARCH_DATABASE"

// recorded is a search that remembers what it was asked and answers what
// the test gave it.
type recorded struct {
	userID, query string
	scope         documents.Scope
	topK, calls   int
	hits          []documents.Hit
	err           error
}

func (r *recorded) Search(_ context.Context, userID, query string, scope documents.Scope, topK int) ([]documents.Hit, error) {
	r.userID, r.query, r.scope, r.topK = userID, query, scope, topK
	r.calls++
	return r.hits, r.err
}

func post(t *testing.T, route Route, userID, body string) (int, string) {
	t.Helper()
	route.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Register(api)
	request := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(body))
	request = request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func TestASearchIsTheChatsSearchAskedAsTheCaller(t *testing.T) {
	page := int64(4)
	search := &recorded{hits: []documents.Hit{
		{DocumentID: 41, Title: "Tide tables", DocumentType: "FILE", Metadata: []byte(`{"path":"/srv/private"}`),
			Chunks: []documents.Chunk{{ID: 900, Content: "High water at six.", Position: 2, Page: &page, DocumentID: 41}}},
		{DocumentID: 42, Title: "Shore notes", DocumentType: "NOTE"},
	}}
	status, body := post(t, Route{Search: search}, testpostgres.Alice, `{"query":"  high water ","top_k":3}`)
	want := `{"items":[{"document_id":41,"title":"Tide tables","document_type":"FILE","rank":1,"chunks":[{"chunk_id":900,"content":"High water at six.","position":2,"page":4}]},{"document_id":42,"title":"Shore notes","document_type":"NOTE","rank":2,"chunks":[]}]}` + "\n"
	if status != 200 || body != want {
		t.Errorf("%d %s", status, body)
	}
	if search.userID != testpostgres.Alice || search.query != "high water" || search.topK != 3 || search.scope.Limited {
		t.Errorf("the search was asked %+v", search)
	}

	// Nothing found is an empty list, and five documents is what is asked
	// for when the caller does not say.
	search = &recorded{}
	if status, body := post(t, Route{Search: search}, testpostgres.Alice, `{"query":"x"}`); status != 200 || body != `{"items":[]}`+"\n" || search.topK != defaultTopK {
		t.Errorf("%d %s, top_k %d", status, body, search.topK)
	}
}

func TestARequestThatIsNotASearch(t *testing.T) {
	tests := []struct {
		name, body string
		says       []string
	}{
		{"no query", `{}`, []string{"query"}},
		{"a blank query", `{"query":"   "}`, []string{"query"}},
		{"a query that is a document", `{"query":"` + strings.Repeat("a", 1001) + `"}`, []string{"query"}},
		{"more documents than a search answers", `{"query":"x","top_k":21}`, []string{"top_k"}},
		{"no documents", `{"query":"x","top_k":0}`, []string{"top_k"}},
		{"a type that does not exist, and no query", `{"document_types":["MEMO"]}`, []string{"query", "document_types", "MEMO"}},
		{"not JSON", `query=x`, []string{"body"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			search := &recorded{}
			status, body := post(t, Route{Search: search}, testpostgres.Alice, test.body)
			if status != 422 || search.calls != 0 {
				t.Fatalf("%d %s, searched %d times", status, body, search.calls)
			}
			for _, piece := range test.says {
				if !strings.Contains(body, piece) {
					t.Errorf("%s lacks %s", body, piece)
				}
			}
		})
	}
}

func TestAFailedSearchSaysNothingOfWhy(t *testing.T) {
	search := &recorded{err: errors.New("qdrant at 10.0.0.7 refused the key sk-123")}
	status, body := post(t, Route{Search: search}, testpostgres.Alice, `{"query":"x"}`)
	if status != 500 || strings.Contains(body, "qdrant") || strings.Contains(body, "sk-123") {
		t.Errorf("%d %s", status, body)
	}
}

// keywordsOnly is the search's vector half, switched off: the embedder
// answers a vector and the index finds no neighbour, so what a search
// returns is the keyword leg's, read from Postgres by the chat's own store.
type keywordsOnly struct{}

func (keywordsOnly) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }
func (keywordsOnly) Nearest(context.Context, string, []float32, []int64, int) ([]documents.Neighbor, error) {
	return nil, nil
}

// TestASearchReadsOnlyTheCallersDocuments runs the route over the chat's
// real searcher and a real Postgres: two accounts hold a passage with the
// same words, and each finds its own and never the other's, with and
// without a type filter.
func TestASearchReadsOnlyTheCallersDocuments(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// The text is made up for the test.
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (41, '`+alice+`', 'Tide tables', 'FILE', '', 'h41', '{"state":"ready"}'),
 (42, '`+alice+`', 'Shore notes', 'NOTE', '', 'h42', '{"state":"ready"}'),
 (44, '`+alice+`', 'Going', 'NOTE', '', 'h44', '{"state":"deleting"}'),
 (51, '`+bob+`',   'His tides', 'NOTE', '', 'h51', '{"state":"ready"}');
INSERT INTO chunks (id, document_id, user_id, content, "position") VALUES
 (900, 41, '`+alice+`', 'Spring tides follow the full moon.', 0),
 (901, 42, '`+alice+`', 'Kelp marks the spring tides line.', 0),
 (902, 44, '`+alice+`', 'Spring tides, in a note being deleted.', 0),
 (950, 51, '`+bob+`',   'His secret spring tides.', 0);`)

	searcher, err := documents.NewSearcher(keywordsOnly{}, keywordsOnly{}, documents.Store{Pool: pool},
		documents.Limits{Candidates: 50, MaxDocuments: 20, ContextChars: 10000, DocumentChars: 5000})
	if err != nil {
		t.Fatal(err)
	}
	route := Route{Pool: pool, Search: searcher}

	tests := []struct {
		name, user, body string
		says, lacks      []string
	}{
		{"her search finds both of hers", alice, `{"query":"spring tides"}`,
			[]string{`"document_id":41`, `"document_id":42`}, []string{"secret", "His", "being deleted"}},
		{"his search finds his", bob, `{"query":"spring tides"}`, []string{`"document_id":51`, "His secret"}, []string{"Kelp", "full moon"}},
		{"only her notes", alice, `{"query":"spring tides","document_types":["NOTE","NOTE"]}`,
			[]string{`"document_id":42`, `"rank":1`}, []string{`"document_id":41`, "secret", "being deleted"}},
		{"a type she has nothing of", alice, `{"query":"spring tides","document_types":["YOUTUBE_VIDEO"]}`,
			[]string{`{"items":[]}`}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := post(t, route, test.user, test.body)
			if status != 200 {
				t.Fatalf("%d %s", status, body)
			}
			for _, piece := range test.says {
				if !strings.Contains(body, piece) {
					t.Errorf("%s lacks %s", body, piece)
				}
			}
			for _, piece := range test.lacks {
				if strings.Contains(body, piece) {
					t.Errorf("%s should not say %s", body, piece)
				}
			}
		})
	}
}
