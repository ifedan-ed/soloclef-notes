package academic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// The records below are hand-written in the sources' formats, with invented
// papers: enough of each format to hold the reader to the parts it reads.

const pubmedRecords = `<?xml version="1.0"?>
<!DOCTYPE PubmedArticleSet PUBLIC "-//NLM//DTD PubMedArticle, 1st January 2025//EN" "https://dtd.nlm.nih.gov/ncbi/pubmed/out/pubmed_250101.dtd">
<PubmedArticleSet>
<PubmedArticle><MedlineCitation><PMID Version="1">222</PMID><Article>
  <Journal><Title>Journal of Second Things</Title><JournalIssue><PubDate><MedlineDate>2019 Nov-Dec</MedlineDate></PubDate></JournalIssue></Journal>
  <ArticleTitle>Second paper.</ArticleTitle>
  <AuthorList><Author><CollectiveName>The Study Group</CollectiveName></Author></AuthorList>
</Article></MedlineCitation><PubmedData><ArticleIdList><ArticleId IdType="pubmed">222</ArticleId></ArticleIdList></PubmedData></PubmedArticle>
<PubmedArticle><MedlineCitation><PMID Version="1">111</PMID><Article>
  <Journal><Title>Journal of First Things</Title><JournalIssue><PubDate><Year>2024</Year></PubDate></JournalIssue></Journal>
  <ArticleTitle>Effects of <i>E. coli</i> &amp; friends on H<sub>2</sub>O.</ArticleTitle>
  <Abstract><AbstractText Label="BACKGROUND">Little is known.</AbstractText><AbstractText Label="RESULTS">Much was <b>found</b>.</AbstractText></Abstract>
  <AuthorList><Author><LastName>Okafor</LastName><Initials>AB</Initials></Author><Author><LastName>Lindqvist</LastName><Initials>C</Initials></Author><Author><LastName>Tanaka</LastName><Initials>D</Initials></Author><Author><LastName>Moreau</LastName><Initials>E</Initials></Author></AuthorList>
</Article></MedlineCitation><PubmedData><ArticleIdList><ArticleId IdType="doi">10.1000/x</ArticleId><ArticleId IdType="pmc">PMC777</ArticleId></ArticleIdList></PubmedData></PubmedArticle>
</PubmedArticleSet>`

const pmcArticle = `<pmc-articleset><article><front><article-meta><title-group><article-title>Front matter title</article-title></title-group></article-meta></front>
<body><sec><title>Methods</title><p>We measured <italic>things</italic> carefully<xref ref-type="bibr">1</xref>.</p>
<table-wrap><table><tr><td>cell that is not prose</td></tr></table></table-wrap>
<sec><title>Nested</title><p>A nested paragraph.</p></sec></sec></body>
<back><ref-list><ref>Reference that is not prose.</ref></ref-list></back></article></pmc-articleset>`

const arxivFeed = `<?xml version='1.0' encoding='UTF-8'?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <title>arXiv Query</title>
  <entry><id>http://arxiv.org/abs/2401.00001v2</id><title>A Paper
   Over Two Lines</title><summary>  Its summary.
  </summary><published>2024-01-02T00:00:00Z</published>
  <author><name>Ada Example</name></author><author><name>Ben Example</name></author>
  <arxiv:primary_category term="cs.LG"/></entry>
</feed>`

const emptyFeed = `<?xml version='1.0'?><feed xmlns="http://www.w3.org/2005/Atom"><title>arXiv Query</title></feed>`

const europePMC = `{"resultList":{"result":[
 {"doi":"10.64898/2026.01.02.000001","title":"A <i>new</i> preprint","authorString":"Ng A, Silva B.","abstractText":"<h4>Abstract</h4> It works.","firstPublicationDate":"2026-01-03","bookOrReportDetails":{"publisher":"bioRxiv"}},
 {"doi":"10.1101/2020.05.05.20000001","title":"An older one","authorString":"Khan C.","firstPublicationDate":"2020-05-06","bookOrReportDetails":{"publisher":"medRxiv"}},
 {"doi":"10.5555/else","title":"From another server","bookOrReportDetails":{"publisher":"Research Square"}}]}}`

// fake serves the three sources from one address and records the requests.
type fake struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	arxiv    []string // replies in order
	failures int      // answer 429 this many times first
}

func newFake(t *testing.T) (*fake, *Sources) {
	f := &fake{arxiv: []string{arxivFeed}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r)
		fail := f.failures > 0
		if fail {
			f.failures--
		}
		f.mu.Unlock()
		if fail {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		q := r.URL.Query()
		switch {
		case strings.HasSuffix(r.URL.Path, "/esearch.fcgi"):
			_, _ = w.Write([]byte(`{"esearchresult":{"idlist":["111","222"]}}`))
		case strings.HasSuffix(r.URL.Path, "/efetch.fcgi") && q.Get("db") == "pubmed":
			_, _ = w.Write([]byte(pubmedRecords))
		case strings.HasSuffix(r.URL.Path, "/efetch.fcgi") && q.Get("db") == "pmc":
			_, _ = w.Write([]byte(pmcArticle))
		case r.URL.Path == "/arxiv":
			f.mu.Lock()
			reply := f.arxiv[0]
			if len(f.arxiv) > 1 {
				f.arxiv = f.arxiv[1:]
			}
			f.mu.Unlock()
			_, _ = w.Write([]byte(reply))
		case r.URL.Path == "/epmc":
			_, _ = w.Write([]byte(europePMC))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	s, err := New(Settings{HTTP: f.Client(), NCBITool: "soloclef-test", NCBIEmail: "ops@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	s.eutils, s.arxiv, s.europePMC = f.URL, f.URL+"/arxiv", f.URL+"/epmc"
	s.retryPause = func(int) time.Duration { return 0 }
	return f, s
}

func (f *fake) query(i int) map[string][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i].URL.Query()
}

func TestPubMedReturnsPapersInRelevanceOrderWithTheirParts(t *testing.T) {
	f, s := newFake(t)
	papers, err := s.PubMed(context.Background(), "e coli water", 5, false, 2020, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The fetch answered 222 first; the search said 111 is more relevant.
	if len(papers) != 2 || papers[0].URL != "https://pubmed.ncbi.nlm.nih.gov/111/" {
		t.Fatalf("papers = %+v", papers)
	}
	first := papers[0]
	if first.Title != "Effects of E. coli & friends on H2O." {
		t.Errorf("title = %q", first.Title)
	}
	if first.Abstract != "BACKGROUND: Little is known. RESULTS: Much was found." {
		t.Errorf("abstract = %q", first.Abstract)
	}
	if first.Year != "2024" || first.Venue != "Journal of First Things" || first.PMCID != "PMC777" || len(first.Authors) != 4 || first.Authors[0] != "Okafor AB" {
		t.Errorf("first = %+v", first)
	}
	if papers[1].Year != "2019" || papers[1].Authors[0] != "The Study Group" {
		t.Errorf("second = %+v", papers[1])
	}
	search := f.query(0)
	if search["term"][0] != `(e coli water) AND ("2020"[pdat] : "3000"[pdat])` || search["tool"][0] != "soloclef-test" || search["email"][0] != "ops@example.org" || search["sort"][0] != "relevance" {
		t.Errorf("search query = %v", search)
	}
	if _, sent := search["api_key"]; sent {
		t.Error("an api_key was sent though none is set")
	}
	if got := first.meta(); got != "Okafor AB, Lindqvist C, Tanaka D et al. · Journal of First Things · 2024" {
		t.Errorf("meta = %q", got)
	}
}

func TestPubMedFullTextIsTheBodysProseOnly(t *testing.T) {
	_, s := newFake(t)
	papers, err := s.PubMed(context.Background(), "x", 5, true, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The citation marker, the table and the reference list are not prose.
	want := "Methods\nWe measured things carefully.\nNested\nA nested paragraph."
	if got := papers[0].FullText; got != want {
		t.Fatalf("full text = %q", got)
	}
	if papers[1].FullText != "" {
		t.Fatal("a paper that is not in PubMed Central was given full text")
	}
}

func TestJATSBodyIsCutAtItsBudgetAndEmptyWithoutABody(t *testing.T) {
	if got := jatsBody([]byte(pmcArticle), 10); len([]rune(got)) > 11 {
		t.Fatalf("a body cut to 10 characters is %d long: %q", len([]rune(got)), got)
	}
	if got := jatsBody([]byte(`<article><front><article-title>Only front</article-title></front></article>`), 100); got != "" {
		t.Fatalf("front matter was read as a body: %q", got)
	}
	if got := jatsBody([]byte(`not xml at all <<<`), 100); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestArxivRequiresEveryWordThenFallsBackToAnyWord(t *testing.T) {
	f, s := newFake(t)
	f.arxiv = []string{emptyFeed, arxivFeed}
	papers, err := s.Arxiv(context.Background(), `graph "neural" networks`, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.query(0)["search_query"][0]; got != "all:graph AND all:neural AND all:networks" {
		t.Errorf("first query = %q", got)
	}
	if got := f.query(1)["search_query"][0]; got != "all:graph OR all:neural OR all:networks" {
		t.Errorf("second query = %q", got)
	}
	if len(papers) != 1 {
		t.Fatalf("papers = %+v", papers)
	}
	p := papers[0]
	if p.Title != "A Paper Over Two Lines" || p.URL != "https://arxiv.org/abs/2401.00001v2" || p.Year != "2024" || p.Venue != "arXiv cs.LG" || p.Abstract != "Its summary." || len(p.Authors) != 2 {
		t.Fatalf("paper = %+v", p)
	}
}

// A one-word query that finds nothing is not asked twice: AND and OR of one
// word are the same question.
func TestArxivDoesNotRepeatAOneWordQuery(t *testing.T) {
	f, s := newFake(t)
	f.arxiv = []string{emptyFeed}
	if papers, err := s.Arxiv(context.Background(), "zzzz", 5); err != nil || len(papers) != 0 {
		t.Fatalf("papers=%v err=%v", papers, err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("%d requests for a one-word query", len(f.requests))
	}
}

// bioRxiv and medRxiv changed DOI prefix in 2025. A preprint is recognised
// by the server the index names, so one with the new prefix is kept and one
// from another server is not.
func TestPreprintsAreRecognisedByServerNotByDOIPrefix(t *testing.T) {
	f, s := newFake(t)
	papers, err := s.Preprints(context.Background(), "plasmid", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 2 {
		t.Fatalf("papers = %+v", papers)
	}
	if papers[0].URL != "https://doi.org/10.64898/2026.01.02.000001" || papers[0].Venue != "bioRxiv (preprint)" || papers[0].Title != "A new preprint" || papers[0].Abstract != "Abstract It works." || papers[0].Year != "2026" {
		t.Errorf("first = %+v", papers[0])
	}
	if len(papers[0].Authors) != 2 || papers[0].Authors[1] != "Silva B" {
		t.Errorf("authors = %q", papers[0].Authors)
	}
	if got := f.query(0)["query"][0]; got != `(plasmid) AND SRC:PPR AND (PUBLISHER:"medRxiv" OR PUBLISHER:"bioRxiv")` {
		t.Errorf("query = %q", got)
	}
	if _, err := s.Preprints(context.Background(), "x", "medrxiv", 10); err != nil {
		t.Fatal(err)
	}
	if got := f.query(1)["query"][0]; got != `(x) AND SRC:PPR AND PUBLISHER:"medRxiv"` {
		t.Errorf("one-server query = %q", got)
	}
	if _, err := s.Preprints(context.Background(), "x", "arxiv", 10); err == nil {
		t.Error("an unknown server was accepted")
	}
}

func TestABusySourceIsRetriedAndAPersistentlyBusyOneIsReported(t *testing.T) {
	f, s := newFake(t)
	f.failures = 2
	if _, err := s.Preprints(context.Background(), "x", "", 5); err != nil {
		t.Fatalf("two refusals then an answer should succeed: %v", err)
	}
	f.failures = 3
	if _, err := s.Preprints(context.Background(), "x", "", 5); err == nil || !strings.Contains(err.Error(), "answered 429") {
		t.Fatalf("err = %v", err)
	}
}

// The three sources are three tools, and each cites each paper by address.
func TestTheSourcesAreSeparateToolsThatCiteEachPaper(t *testing.T) {
	_, s := newFake(t)
	tools := s.Tools()
	names := map[string]tool.Tool{}
	for _, tl := range tools {
		if err := tl.Validate(); err != nil {
			t.Fatal(err)
		}
		if tl.Approval != tool.Auto {
			t.Errorf("%s asks for approval to read public data", tl.Name)
		}
		names[tl.Name] = tl
	}
	if len(names) != 3 || names[PubMedTool].Run == nil || names[ArxivTool].Run == nil || names[PreprintTool].Run == nil {
		t.Fatalf("tools = %v", names)
	}
	reg := citation.NewRegistry()
	out, err := names[ArxivTool].Run(context.Background(), tool.Call{Args: json.RawMessage(`{"query":"paper"}`), Citations: reg})
	if err != nil {
		t.Fatal(err)
	}
	want := "[1] A Paper Over Two Lines\nhttps://arxiv.org/abs/2401.00001v2\nAda Example, Ben Example · arXiv cs.LG · 2024\nAbstract: Its summary."
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if src, _ := reg.Lookup(1); src.Kind != "web_result" || src.Ref != "https://arxiv.org/abs/2401.00001v2" {
		t.Fatalf("source = %+v", src)
	}
	if _, err := names[PubMedTool].Run(context.Background(), tool.Call{Args: json.RawMessage(`{"query":" "}`), Citations: reg}); err == nil {
		t.Fatal("an empty query was searched")
	}
}

func TestThePacerSpacesCallersAndStopsWaitingWhenCancelled(t *testing.T) {
	p := newPacer(map[string]time.Duration{"slow.example": 40 * time.Millisecond})
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := p.wait(context.Background(), "slow.example"); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("three requests went out in %s; the third must be 80ms after the first", elapsed)
	}
	if err := p.wait(context.Background(), "unlisted.example"); err != nil {
		t.Fatal(err)
	}
	slow := newPacer(map[string]time.Duration{"h": time.Hour})
	_ = slow.wait(context.Background(), "h")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := slow.wait(ctx, "h"); err == nil {
		t.Fatal("a cancelled wait went on waiting")
	}
}

func TestNewRefusesMissingSettings(t *testing.T) {
	if _, err := New(Settings{NCBITool: "x"}); err == nil {
		t.Error("no HTTP client was accepted")
	}
	if _, err := New(Settings{HTTP: http.DefaultClient}); err == nil {
		t.Error("no NCBI tool name was accepted")
	}
}

// The real sources, opt-in: they are public and keyless, but a test suite
// must not depend on the internet or lean on their rate limits by default.
func TestLiveSources(t *testing.T) {
	if os.Getenv("SOLOCLEF_API_TEST_LIVE_ACADEMIC") != "1" {
		t.Skip("set SOLOCLEF_API_TEST_LIVE_ACADEMIC=1 to query PubMed, arXiv and Europe PMC")
	}
	s, err := New(Settings{HTTP: &http.Client{Timeout: 30 * time.Second}, NCBITool: "soloclef-test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, search := range map[string]func() ([]Paper, error){
		"pubmed":    func() ([]Paper, error) { return s.PubMed(ctx, "CRISPR base editing", 3, true, 0, 0) },
		"arxiv":     func() ([]Paper, error) { return s.Arxiv(ctx, "attention transformer", 3) },
		"preprints": func() ([]Paper, error) { return s.Preprints(ctx, "CRISPR", "", 3) },
	} {
		start := time.Now()
		papers, err := search()
		if err != nil || len(papers) == 0 {
			t.Errorf("%s: %d papers, %v", name, len(papers), err)
			continue
		}
		t.Logf("%s: %d papers in %s; first: %s (%s), full text %d chars", name, len(papers), time.Since(start).Round(time.Millisecond), papers[0].Title, papers[0].URL, len(papers[0].FullText)+len(papers[len(papers)-1].FullText)+len(papers[1].FullText))
	}
}

// A paper's open body on its own, as importing it as a source reads it.
func TestOpenFullTextIsThePapersBodyUpToItsLimit(t *testing.T) {
	f, s := newFake(t)
	body, err := s.OpenFullText(context.Background(), "PMC900", 100_000)
	if err != nil || body != "Methods\nWe measured things carefully.\nNested\nA nested paragraph." {
		t.Fatalf("body = %q, %v", body, err)
	}
	if q := f.query(0); q["db"][0] != "pmc" || q["id"][0] != "PMC900" || q["tool"][0] != "soloclef-test" {
		t.Errorf("asked %v", q)
	}
	if short, _ := s.OpenFullText(context.Background(), "PMC900", 10); len(short) >= len(body) {
		t.Errorf("a limit of 10 gave %q", short)
	}
}
