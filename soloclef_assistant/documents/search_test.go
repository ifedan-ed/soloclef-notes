package documents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// The two accounts of these tests.
const (
	ada = "11111111-1111-4111-8111-111111111111"
	bob = "22222222-2222-4222-8222-222222222222"
)

// corpus is an account's chunks in memory, behind the same interfaces the
// stores implement, so a search can be followed end to end by hand.
type corpus struct {
	chunks   []Chunk
	owner    map[int64]string // chunk id -> account
	deleting map[int64]bool   // document id
	folders  map[int64][]int64

	nearest  []Neighbor
	keywords []Ranked

	embedErr, indexErr error
	indexScope         []int64
	indexCalled        bool
}

func (c *corpus) Embed(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2}, c.embedErr
}

func (c *corpus) Nearest(_ context.Context, _ string, _ []float32, chunkIDs []int64, limit int) ([]Neighbor, error) {
	c.indexCalled, c.indexScope = true, chunkIDs
	if c.indexErr != nil {
		return nil, c.indexErr
	}
	return c.nearest[:min(limit, len(c.nearest))], nil
}

func (c *corpus) KeywordRanks(context.Context, string, string, []int64, int) ([]Ranked, error) {
	return c.keywords, nil
}

func (c *corpus) Chunks(_ context.Context, userID string, ids, documentIDs []int64) ([]Chunk, error) {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	inScope := map[int64]bool{}
	for _, id := range documentIDs {
		inScope[id] = true
	}
	var out []Chunk
	for _, ch := range c.chunks {
		if want[ch.ID] && c.owner[ch.ID] == userID && !c.deleting[ch.DocumentID] &&
			(len(documentIDs) == 0 || inScope[ch.DocumentID]) {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (c *corpus) ChunkIDsOfDocuments(_ context.Context, _ string, documentIDs []int64) ([]int64, error) {
	var out []int64
	for _, ch := range c.chunks {
		for _, d := range documentIDs {
			if ch.DocumentID == d {
				out = append(out, ch.ID)
			}
		}
	}
	return out, nil
}

func (c *corpus) DocumentsInFolders(_ context.Context, _ string, folderIDs []int64) ([]int64, error) {
	var out []int64
	for _, f := range folderIDs {
		out = append(out, c.folders[f]...)
	}
	return out, nil
}

var roomy = Limits{Candidates: 200, MaxDocuments: 20, ContextChars: 26000, DocumentChars: 16000}

func searcher(t *testing.T, c *corpus, limits Limits) *Searcher {
	t.Helper()
	s, err := NewSearcher(c, c, c, limits)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A small library: two documents of Ada's, one of Bob's.
func library() *corpus {
	return &corpus{
		chunks: []Chunk{
			{ID: 10, DocumentID: 1, Title: "Cell biology", DocumentType: "FILE", Position: 0, Content: "Mitosis has four phases."},
			{ID: 11, DocumentID: 1, Title: "Cell biology", DocumentType: "FILE", Position: 1, Content: "Prophase comes first."},
			{ID: 20, DocumentID: 2, Title: "Genetics", DocumentType: "NOTE", Position: 0, Content: "Meiosis halves the chromosome number."},
			{ID: 90, DocumentID: 9, Title: "Somebody else's", DocumentType: "FILE", Position: 0, Content: "Private to Bob."},
		},
		owner: map[int64]string{10: ada, 11: ada, 20: ada, 90: bob},
	}
}

func TestFusionRanksAChunkBothLegsFoundAboveOneOnlyOneDid(t *testing.T) {
	// 11 is second in both legs; 10 and 20 are first in one leg each.
	fused := fuse(
		[]Neighbor{{ChunkID: 10, Score: 0.9}, {ChunkID: 11, Score: 0.8}},
		[]Ranked{{ChunkID: 20, Rank: 1}, {ChunkID: 11, Rank: 2}},
		10)
	got := []int64{fused[0].chunkID, fused[1].chunkID, fused[2].chunkID}
	if got[0] != 11 || got[1] != 10 || got[2] != 20 {
		t.Fatalf("order = %v, want [11 10 20]: 2/(60+2) beats 1/(60+1), and a tie goes to the lower id", got)
	}
}

func TestFusionGivesEqualScoresOneRankAndCutsToTheWindow(t *testing.T) {
	fused := fuse([]Neighbor{{ChunkID: 1, Score: 0.5}, {ChunkID: 2, Score: 0.5}, {ChunkID: 3, Score: 0.4}}, nil, 2)
	if len(fused) != 2 {
		t.Fatalf("window not applied: %d", len(fused))
	}
	if fused[0].score != fused[1].score || fused[0].bestRank != 1 || fused[1].bestRank != 1 {
		t.Fatalf("tied scores did not share rank 1: %+v", fused)
	}
	// The third would have been rank 3, not 2: ranks skip after a tie.
	all := fuse([]Neighbor{{ChunkID: 1, Score: 0.5}, {ChunkID: 2, Score: 0.5}, {ChunkID: 3, Score: 0.4}}, nil, 3)
	if all[2].bestRank != 3 {
		t.Fatalf("rank after a tie = %d, want 3", all[2].bestRank)
	}
}

func TestSearchGroupsPassagesUnderTheirDocumentsInReadingOrder(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 11, Score: 0.9}, {ChunkID: 20, Score: 0.8}, {ChunkID: 10, Score: 0.7}}
	hits, err := searcher(t, c, roomy).Search(context.Background(), ada, "mitosis", Scope{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].DocumentID != 1 || hits[1].DocumentID != 2 {
		t.Fatalf("documents = %+v", hits)
	}
	if hits[0].Chunks[0].ID != 10 || hits[0].Chunks[1].ID != 11 {
		t.Fatalf("passages are not in the document's order: %v, %v", hits[0].Chunks[0].ID, hits[0].Chunks[1].ID)
	}
}

// Each passage carries how close the vector store found it, and 0 when only
// the keyword leg did: a caller that must not show a far neighbour (the
// notebooks home's search box) has nothing else to tell one by.
func TestEachPassageSaysHowCloseTheVectorLegFoundIt(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 11, Score: 0.82}, {ChunkID: 10, Score: 0.31}}
	c.keywords = []Ranked{{ChunkID: 20, Rank: 1}, {ChunkID: 10, Rank: 2}}
	hits, err := searcher(t, c, roomy).Search(context.Background(), ada, "mitosis", Scope{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]float64{}
	for _, hit := range hits {
		for _, chunk := range hit.Chunks {
			got[chunk.ID] = chunk.Similarity
		}
	}
	if got[11] != 0.82 || got[10] != 0.31 || got[20] != 0 || len(got) != 3 {
		t.Fatalf("similarities = %v, want 11: 0.82, 10: 0.31 and 20 (keyword only): 0", got)
	}
}

// The vector store is not trusted about whose a chunk is. Here it answers a
// chunk of Bob's to a search of Ada's.
func TestAChunkOfAnotherAccountIsNeverShown(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 90, Score: 0.99}, {ChunkID: 10, Score: 0.5}}
	hits, err := searcher(t, c, roomy).Search(context.Background(), ada, "private", Scope{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].DocumentID != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	if strings.Contains(Render(hits, citation.NewRegistry()), "Private") {
		t.Fatal("another account's text reached the model")
	}
}

func TestADocumentBeingDeletedIsNotSearched(t *testing.T) {
	c := library()
	c.deleting = map[int64]bool{1: true}
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}, {ChunkID: 20, Score: 0.8}}
	hits, _ := searcher(t, c, roomy).Search(context.Background(), ada, "x", Scope{}, 5)
	if len(hits) != 1 || hits[0].DocumentID != 2 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestMentionsConfineTheSearchInBothLegs(t *testing.T) {
	c := library()
	c.folders = map[int64][]int64{7: {2}}
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}, {ChunkID: 20, Score: 0.8}}
	hits, err := searcher(t, c, roomy).Search(context.Background(), ada, "x", Scope{FolderIDs: []int64{7}}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.indexScope) != 1 || c.indexScope[0] != 20 {
		t.Fatalf("the vector store was asked about points %v, want only the folder's chunk 20", c.indexScope)
	}
	if len(hits) != 1 || hits[0].DocumentID != 2 {
		t.Fatalf("hits = %+v", hits)
	}
}

// A mention that names nothing searchable is "nothing found", decided
// without asking either store: it must not widen to everything the account has.
func TestAnEmptyMentionedFolderFindsNothingAndAsksNoStore(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}}
	hits, err := searcher(t, c, roomy).Search(context.Background(), ada, "x", Scope{FolderIDs: []int64{404}}, 5)
	if err != nil || hits != nil || c.indexCalled {
		t.Fatalf("hits=%v err=%v indexCalled=%v", hits, err, c.indexCalled)
	}
}

// A notebook chat: the limit is the notebook's enabled sources. The vector
// store is asked about the limit's points only, and the chunks are read back
// under the limit, so a neighbour from elsewhere in the account that the
// vector store returned anyway is dropped.
func TestALimitConfinesTheSearchToItsDocuments(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}, {ChunkID: 20, Score: 0.8}}
	hits, outcome, err := searcher(t, c, roomy).SearchScoped(context.Background(), ada, "x", Scope{Limited: true, Limit: []int64{2}}, 5)
	if err != nil || outcome != Searched {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if len(c.indexScope) != 1 || c.indexScope[0] != 20 {
		t.Fatalf("the vector store was asked about points %v, want only document 2's chunk 20", c.indexScope)
	}
	if len(hits) != 1 || hits[0].DocumentID != 2 {
		t.Fatalf("a document outside the notebook was returned: %+v", hits)
	}
}

// The bug this milestone starts with: a notebook with no enabled sources
// has an empty limit, and an empty limit must search nothing. Read as "no
// limit" it would search everything the account has.
func TestAnEmptyLimitSearchesNothingNotEverything(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}}
	for _, limit := range [][]int64{nil, {}} {
		hits, outcome, err := searcher(t, c, roomy).SearchScoped(context.Background(), ada, "x", Scope{Limited: true, Limit: limit}, 5)
		if err != nil || hits != nil || outcome != NoSources || c.indexCalled {
			t.Fatalf("limit=%v: hits=%v outcome=%v err=%v indexCalled=%v", limit, hits, outcome, err, c.indexCalled)
		}
	}
}

// A mention narrows a limit and never widens it.
func TestAMentionNarrowsALimitAndCannotWidenIt(t *testing.T) {
	c := library()
	c.folders = map[int64][]int64{7: {1, 2}}
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}, {ChunkID: 20, Score: 0.8}}
	s := searcher(t, c, roomy)

	// The folder holds documents 1 and 2; the notebook has only 2.
	hits, outcome, err := s.SearchScoped(context.Background(), ada, "x", Scope{FolderIDs: []int64{7}, Limited: true, Limit: []int64{2}}, 5)
	if err != nil || outcome != Searched || len(hits) != 1 || hits[0].DocumentID != 2 {
		t.Fatalf("hits=%+v outcome=%v err=%v", hits, outcome, err)
	}

	// A mention wholly outside the notebook is said to be outside it. It is
	// not answered from the rest of the notebook.
	c.indexCalled = false
	hits, outcome, err = s.SearchScoped(context.Background(), ada, "x", Scope{DocumentIDs: []int64{1}, Limited: true, Limit: []int64{2}}, 5)
	if err != nil || hits != nil || outcome != OutsideLimit || c.indexCalled {
		t.Fatalf("hits=%v outcome=%v err=%v indexCalled=%v", hits, outcome, err, c.indexCalled)
	}
}

func TestTheToolSaysWhichKindOfNothingItFound(t *testing.T) {
	c := library()
	s := searcher(t, c, roomy)
	run := func(scope Scope) string {
		t.Helper()
		out, err := s.Tool(ada, scope).Run(context.Background(), tool.Call{Args: json.RawMessage(`{"query":"zebra"}`), Citations: citation.NewRegistry()})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := run(Scope{Limited: true}); !strings.Contains(out, "no enabled sources") {
		t.Fatalf("an empty notebook: %q", out)
	}
	if out := run(Scope{DocumentIDs: []int64{1}, Limited: true, Limit: []int64{2}}); !strings.Contains(out, "not among this notebook's enabled sources") {
		t.Fatalf("a mention outside the notebook: %q", out)
	}
	if out := run(Scope{Limited: true, Limit: []int64{2}}); !strings.Contains(out, "No matches found in this notebook's enabled sources") {
		t.Fatalf("a notebook search with no match: %q", out)
	}
}

func TestBudgetsSkipAChunkThatDoesNotFitAndKeepASmallerOne(t *testing.T) {
	c := &corpus{
		chunks: []Chunk{
			{ID: 1, DocumentID: 1, Title: "A", Position: 0, Content: strings.Repeat("a", 60)},
			{ID: 2, DocumentID: 1, Title: "A", Position: 1, Content: strings.Repeat("b", 60)},
			{ID: 3, DocumentID: 1, Title: "A", Position: 2, Content: "short"},
			{ID: 4, DocumentID: 2, Title: "B", Position: 0, Content: strings.Repeat("c", 500)},
		},
		owner:   map[int64]string{1: ada, 2: ada, 3: ada, 4: ada},
		nearest: []Neighbor{{ChunkID: 1, Score: 0.9}, {ChunkID: 2, Score: 0.8}, {ChunkID: 3, Score: 0.7}, {ChunkID: 4, Score: 0.6}},
	}
	limits := Limits{Candidates: 10, MaxDocuments: 5, ContextChars: 200, DocumentChars: 100}
	hits, _ := searcher(t, c, limits).Search(context.Background(), ada, "x", Scope{}, 5)
	// Document A: chunk 1 (60) fits, chunk 2 would make 120 > 100 and is
	// skipped, chunk 3 (5) still fits. Document B's only chunk is over the
	// search budget, so B is dropped rather than returned empty.
	if len(hits) != 1 || len(hits[0].Chunks) != 2 || hits[0].Chunks[0].ID != 1 || hits[0].Chunks[1].ID != 3 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestTopKIsClampedToTheDeploymentsMaximum(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}, {ChunkID: 20, Score: 0.8}}
	limits := roomy
	limits.MaxDocuments = 1
	hits, _ := searcher(t, c, limits).Search(context.Background(), ada, "x", Scope{}, 50)
	if len(hits) != 1 {
		t.Fatalf("got %d documents past a maximum of 1", len(hits))
	}
}

// A vector store that is down fails the search. It is not answered by
// keyword alone as if that were the whole search.
func TestAVectorStoreFailureFailsTheSearch(t *testing.T) {
	c := library()
	c.indexErr = errors.New("connection refused")
	c.keywords = []Ranked{{ChunkID: 10, Rank: 1}}
	_, err := searcher(t, c, roomy).Search(context.Background(), ada, "x", Scope{}, 5)
	if err == nil || !strings.Contains(err.Error(), "vector store") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderNumbersPassagesInTheThreadsRegistryAndEscapesTitles(t *testing.T) {
	reg := citation.NewRegistry()
	hits := []Hit{{DocumentID: 1, Title: "A \"quoted\"\n<title>", DocumentType: "CRAWLED_URL",
		Metadata: []byte(`{"url":"https://www.example.org/page"}`),
		Chunks:   []Chunk{{ID: 10, Content: " line one\nline two "}, {ID: 11, Content: "other"}}}}
	got := Render(hits, reg)
	for _, want := range []string{
		`<document title="A &quot;quoted&quot; &lt;title&gt;" source="Web · example.org" view="excerpt">`,
		"  [1] line one\n      line two\n",
		"  [2] other\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if src, _ := reg.Lookup(2); src.Kind != "kb_chunk" || src.Ref != "11" {
		t.Fatalf("label 2 is %+v", src)
	}
	// The same passage found again keeps its number.
	if again := Render(hits[:1], reg); !strings.Contains(again, "[1] line one") || len(reg.Sources()) != 2 {
		t.Fatal("a passage shown twice was renumbered")
	}
}

func TestSourceLabel(t *testing.T) {
	for _, c := range []struct{ kind, metadata, want string }{
		{"FILE", "", "File"},
		{"GOOGLE_DRIVE_FILE", "", "Google Drive"},
		{"NOTE", `{"url":"ftp://x"}`, "Note"},
		{"EXTENSION", `{"source_url":"http://news.example.com/a"}`, "Saved page · news.example.com"},
		{"FILE", `[1,2]`, "File"},
	} {
		if got := sourceLabel(c.kind, []byte(c.metadata)); got != c.want {
			t.Errorf("%s %s: got %q want %q", c.kind, c.metadata, got, c.want)
		}
	}
}

func TestTheToolSearchesItsAccountAndSaysWhenNothingMatches(t *testing.T) {
	c := library()
	c.nearest = []Neighbor{{ChunkID: 10, Score: 0.9}}
	s := searcher(t, c, roomy)
	tl := s.Tool(ada, Scope{})
	if err := tl.Validate(); err != nil {
		t.Fatal(err)
	}
	if tl.Approval != tool.Auto || !tl.Parallel {
		t.Fatal("a read-only search must run unasked and beside other calls")
	}
	reg := citation.NewRegistry()
	out, err := tl.Run(context.Background(), tool.Call{Args: json.RawMessage(`{"query":"mitosis"}`), Citations: reg})
	if err != nil || !strings.Contains(out, "[1] Mitosis has four phases.") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := tl.Run(context.Background(), tool.Call{Args: json.RawMessage(`{"query":"  "}`), Citations: reg}); err == nil {
		t.Fatal("an empty query was searched")
	}
	c.nearest = nil
	out, err = tl.Run(context.Background(), tool.Call{Args: json.RawMessage(`{"query":"zebra"}`), Citations: reg})
	if err != nil || !strings.HasPrefix(out, "No matches found") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestQdrantAlwaysFiltersByAccountAndReadsThePoints(t *testing.T) {
	var got map[string]any
	var key, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, path = r.Header.Get("api-key"), r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"result":{"points":[{"id":42,"version":3,"score":0.83},{"id":7,"score":0.5}]},"status":"ok"}`))
	}))
	defer server.Close()
	q := Qdrant{URL: server.URL + "/", APIKey: "k", Collection: "chunks", HNSWEf: 200, Quantized: true, Oversampling: 2, HTTP: server.Client()}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
	// Asked with the uuid in capitals, as a token may carry it: the
	// condition is still the indexer's lower-case form.
	points, err := q.Nearest(context.Background(), strings.ToUpper(ada), []float32{0.5, 0.25}, []int64{42, 7}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].ChunkID != 42 || points[0].Score != 0.83 {
		t.Fatalf("points = %+v", points)
	}
	if path != "/collections/chunks/points/query" || key != "k" {
		t.Fatalf("path=%q key=%q", path, key)
	}
	must := got["filter"].(map[string]any)["must"].([]any)
	tenant := must[0].(map[string]any)
	if tenant["key"] != "user_id" || tenant["match"].(map[string]any)["value"] != ada {
		t.Fatalf("the tenant condition is %v: it must be the indexer's tenant field, user_id, matched to the account's uuid", tenant)
	}
	if ids := must[1].(map[string]any)["has_id"].([]any); len(ids) != 2 {
		t.Fatalf("has_id = %v", ids)
	}
	params := got["params"].(map[string]any)
	if params["hnsw_ef"] != 200.0 || params["quantization"].(map[string]any)["oversampling"] != 2.0 {
		t.Fatalf("params = %v", params)
	}
	if got["with_payload"] != false || got["limit"] != 200.0 {
		t.Fatalf("query = %v", got)
	}
}

func TestQdrantRefusesAnAnswerItCannotTrust(t *testing.T) {
	for name, reply := range map[string]struct {
		status int
		body   string
	}{
		"an error status": {500, `{"status":{"error":"boom"}}`},
		"a uuid point id": {200, `{"result":{"points":[{"id":"6b1f","score":1}]}}`},
		"a body not JSON": {200, `<html>`},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(reply.status)
			_, _ = w.Write([]byte(reply.body))
		}))
		q := Qdrant{URL: server.URL, Collection: "c", HNSWEf: 1, HTTP: server.Client()}
		if _, err := q.Nearest(context.Background(), ada, []float32{1}, nil, 1); err == nil {
			t.Errorf("%s: accepted", name)
		}
		server.Close()
	}
	// A scope with no points is nothing found, without a request.
	q := Qdrant{URL: "http://127.0.0.1:1", Collection: "c", HNSWEf: 1, HTTP: http.DefaultClient}
	if points, err := q.Nearest(context.Background(), ada, []float32{1}, []int64{}, 1); err != nil || points != nil {
		t.Fatalf("points=%v err=%v", points, err)
	}
}

func TestTheEmbedderRefusesAVectorOfTheWrongLength(t *testing.T) {
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" || r.URL.Path != "/v1/embeddings" {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sent)
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer server.Close()
	e := ProxyEmbedder{BaseURL: server.URL + "/v1", Key: "key", Model: "embed", Dimension: 3, HTTP: server.Client()}
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	vector, err := e.Embed(context.Background(), "hello")
	if err != nil || len(vector) != 3 || sent["model"] != "embed" {
		t.Fatalf("vector=%v err=%v sent=%v", vector, err, sent)
	}
	e.Dimension = 1024
	if _, err := e.Embed(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "indexed with") {
		t.Fatalf("a 3-number vector was accepted for a 1024 index: %v", err)
	}
	e.Key = "wrong"
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("a refused request was accepted")
	}
}

func BenchmarkFuse(b *testing.B) {
	neighbors := make([]Neighbor, 200)
	keywords := make([]Ranked, 200)
	for i := range neighbors {
		neighbors[i] = Neighbor{ChunkID: int64(i), Score: 1 - float64(i)/400}
		keywords[i] = Ranked{ChunkID: int64(i + 100), Rank: int64(i + 1)}
	}
	b.ReportAllocs()
	for b.Loop() {
		fuse(neighbors, keywords, 200)
	}
}

// The cost of one vector query apart from the network: encoding 1024 numbers
// and decoding 200 points.
func BenchmarkNearest(b *testing.B) {
	var reply strings.Builder
	reply.WriteString(`{"result":{"points":[`)
	for i := 0; i < 200; i++ {
		if i > 0 {
			reply.WriteByte(',')
		}
		reply.WriteString(`{"id":` + string(rune('1'+i%9)) + `,"version":1,"score":0.5}`)
	}
	reply.WriteString(`]}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, reply.String())
	}))
	defer server.Close()
	q := Qdrant{URL: server.URL, Collection: "chunks", HNSWEf: 200, HTTP: server.Client()}
	vector := make([]float32, 1024)
	for i := range vector {
		vector[i] = float32(i) / 1024
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := q.Nearest(context.Background(), ada, vector, nil, 200); err != nil {
			b.Fatal(err)
		}
	}
}
