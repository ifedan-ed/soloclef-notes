package academicsearch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
)

// sources stands in for the three searches and records what each was asked.
type sources struct {
	asked  []string
	papers []academic.Paper
	err    error
}

func (s *sources) PubMed(_ context.Context, query string, limit int, fullText bool, since, until int) ([]academic.Paper, error) {
	s.asked = append(s.asked, strings.Join([]string{"pubmed", query, itoa(limit), boolText(fullText), itoa(since), itoa(until)}, "|"))
	return s.papers, s.err
}

func (s *sources) Arxiv(_ context.Context, query string, limit int) ([]academic.Paper, error) {
	s.asked = append(s.asked, "arxiv|"+query+"|"+itoa(limit))
	return s.papers, s.err
}

func (s *sources) Preprints(_ context.Context, query, server string, limit int) ([]academic.Paper, error) {
	s.asked = append(s.asked, "preprints|"+query+"|"+server+"|"+itoa(limit))
	return s.papers, s.err
}

func itoa(n int) string { return strconv.Itoa(n) }

func boolText(b bool) string {
	if b {
		return "full"
	}
	return "abstract"
}

func serve(t *testing.T, stand *sources, signedIn bool, path, body string) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Sources: stand, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if signedIn {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: "11111111-1111-4111-8111-111111111111", Method: principal.MethodSession}))
	}
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func TestEachSourceIsAskedItsOwnSearch(t *testing.T) {
	for _, c := range []struct {
		name, path, body, asked string
	}{
		{"pubmed with its defaults", PubMedPath, `{"query":"  sepsis lactate "}`, "pubmed|sepsis lactate|10|abstract|0|0"},
		{"pubmed as the MCP tool sends it, nulls included", PubMedPath,
			`{"query":"sepsis","max_results":30,"since":2019,"until":null,"full_text":true}`, "pubmed|sepsis|30|full|2019|0"},
		{"arxiv", ArxivPath, `{"query":"diffusion","max_results":5}`, "arxiv|diffusion|5"},
		{"both preprint servers", PreprintPath, `{"query":"long covid"}`, "preprints|long covid||10"},
		{"one preprint server, asked of the index", PreprintPath, `{"query":"long covid","max_results":7,"server":"biorxiv"}`, "preprints|long covid|biorxiv|7"},
	} {
		stand := &sources{}
		status, body := serve(t, stand, true, c.path, c.body)
		if status != 200 || strings.TrimSpace(body) != "[]" {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
		if len(stand.asked) != 1 || stand.asked[0] != c.asked {
			t.Errorf("%s: asked %v, want %s", c.name, stand.asked, c.asked)
		}
	}
}

func TestABadRequestIsRefusedBeforeAnySourceIsAsked(t *testing.T) {
	for _, c := range []struct{ name, path, body, names string }{
		{"no query", ArxivPath, `{}`, `"query"`},
		{"a blank query", ArxivPath, `{"query":"   "}`, `"query"`},
		{"a query past the limit", ArxivPath, `{"query":"` + strings.Repeat("a", 501) + `"}`, `"query"`},
		{"too many results", PubMedPath, `{"query":"x","max_results":31}`, `"max_results"`},
		{"no results", PubMedPath, `{"query":"x","max_results":0}`, `"max_results"`},
		{"a year that is not one", PubMedPath, `{"query":"x","since":19}`, `"since"`},
		{"a window that ends before it starts", PubMedPath, `{"query":"x","since":2024,"until":2020}`, `"since"`},
		{"a year window on arXiv, which would be ignored", ArxivPath, `{"query":"x","since":2020}`, `"since"`},
		{"full text on arXiv", ArxivPath, `{"query":"x","full_text":true}`, `"full_text"`},
		{"a server on PubMed", PubMedPath, `{"query":"x","server":"medrxiv"}`, `"server"`},
		{"an unknown server", PreprintPath, `{"query":"x","server":"chemrxiv"}`, `"server"`},
		{"a field nobody reads", ArxivPath, `{"query":"x","mode":"deep"}`, `"body"`},
		{"not JSON", ArxivPath, `query=x`, `"body"`},
	} {
		stand := &sources{}
		status, body := serve(t, stand, true, c.path, c.body)
		if status != 422 || !strings.Contains(body, c.names) {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
		if len(stand.asked) != 0 {
			t.Errorf("%s: a source was asked: %v", c.name, stand.asked)
		}
	}
}

func TestPapersBecomeRowsWithTheIdsTheirSourceGave(t *testing.T) {
	long := strings.Repeat("é", 450)
	stand := &sources{papers: []academic.Paper{
		{Title: "Lactate <2 & outcome", URL: "https://pubmed.ncbi.nlm.nih.gov/31234567/", Authors: []string{"Ng A", "Okafor B", "Li C", "Diaz D"},
			Venue: "Crit Care", Year: "2019", Abstract: long, PMCID: "PMC7654321", FullText: "The body."},
		{Title: "No abstract", URL: "https://pubmed.ncbi.nlm.nih.gov/2/", Year: "2019 Nov"},
	}}
	status, body := serve(t, stand, true, PubMedPath, `{"query":"lactate","full_text":true}`)
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	for _, piece := range []string{
		`"id":"pubmed:https://pubmed.ncbi.nlm.nih.gov/31234567/"`, `"title":"Lactate <2 & outcome"`, `"source":"pubmed"`,
		`"snippet":"The body."`, `"full_text":true`, `"thumbnail_url":null`,
		`"meta":"Ng A, Okafor B, Li C et al. · Crit Care · 2019"`,
		`"pmid":"31234567"`, `"pmcid":"PMC7654321"`, `"doi":null`, `"year":2019`, `"badges":["pubmed","pmc"]`, `"open_access":true`,
		// The second paper: nothing invented where the source gave nothing.
		`"pmid":"2","pmcid":null`, `"authors":[]`, `"year":null`, `"badges":["pubmed"],"open_access":false`,
	} {
		if !strings.Contains(body, piece) {
			t.Errorf("the rows lack %s:\n%s", piece, body)
		}
	}

	stand = &sources{papers: []academic.Paper{{Title: "A", URL: "https://arxiv.org/abs/2401.01234v2", Venue: "arXiv cs.LG", Year: "2024", Abstract: long}}}
	_, body = serve(t, stand, true, ArxivPath, `{"query":"a"}`)
	if !strings.Contains(body, `"arxiv_id":"2401.01234v2"`) || !strings.Contains(body, `"snippet":"`+strings.Repeat("é", 400)+`…"`) {
		t.Errorf("an arXiv row: %s", body)
	}

	stand = &sources{papers: []academic.Paper{{Title: "P", URL: "https://doi.org/10.64898/2026.01.02.123456", Venue: "bioRxiv (preprint)"}}}
	_, body = serve(t, stand, true, PreprintPath, `{"query":"p"}`)
	if !strings.Contains(body, `"doi":"10.64898/2026.01.02.123456"`) || !strings.Contains(body, `"server":"biorxiv"`) || !strings.Contains(body, `"badges":["biorxiv"]`) {
		t.Errorf("a preprint row: %s", body)
	}
}

func TestNoMoreRowsThanWereAskedFor(t *testing.T) {
	stand := &sources{papers: make([]academic.Paper, 5)}
	_, body := serve(t, stand, true, ArxivPath, `{"query":"a","max_results":2}`)
	if got := strings.Count(body, `"source":"arxiv"`); got != 2 {
		t.Errorf("%d rows for max_results 2", got)
	}
}

func TestAFailingSourceIsNamedAndNotA500(t *testing.T) {
	stand := &sources{err: errors.New("export.arxiv.org answered 503")}
	status, body := serve(t, stand, true, ArxivPath, `{"query":"a"}`)
	if status != 502 || !strings.Contains(body, "export.arxiv.org answered 503") {
		t.Errorf("%d %s", status, body)
	}
	stand = &sources{err: context.DeadlineExceeded}
	if status, body = serve(t, stand, true, ArxivPath, `{"query":"a"}`); status != 504 {
		t.Errorf("a slow source: %d %s", status, body)
	}
}

func TestScholarIsNotARouteAndNobodySearchesUnnamed(t *testing.T) {
	stand := &sources{}
	if status, _ := serve(t, stand, true, "/api/v1/academic-search/google_scholar", `{"query":"a"}`); status != 404 {
		t.Errorf("google_scholar answered %d; it is not a source of this tier", status)
	}
	if status, _ := serve(t, stand, false, ArxivPath, `{"query":"a"}`); status != 500 || len(stand.asked) != 0 {
		t.Errorf("a request with no caller: %d, asked %v", status, stand.asked)
	}
}
