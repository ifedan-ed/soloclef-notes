package notebookresearch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

const reader = "11111111-1111-4111-8111-111111111111"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var (
	tidesPaper = academic.Paper{Title: "Tides and the moon", URL: "https://pubmed.ncbi.nlm.nih.gov/111/", Authors: []string{"A One", "B Two", "C Three", "D Four"},
		Venue: "Journal of Tides", Year: "2024", Abstract: "Tides follow the moon.", PMCID: "PMC900"}
	seaPaper   = academic.Paper{Title: "Sea levels", URL: "https://pubmed.ncbi.nlm.nih.gov/222/", Year: "2021", Abstract: "Levels rise."}
	arxivPaper = academic.Paper{Title: "Tidal models", URL: "https://arxiv.org/abs/2401.01234", Venue: "arXiv", Year: "2024", Abstract: "A model."}
	bioPaper   = academic.Paper{Title: "Tidal biology", URL: "https://doi.org/10.1101/2024.01.01.123456", Venue: "bioRxiv (preprint)", Year: "2024", Abstract: "Crabs."}
)

// events runs a search and returns what it emitted, decoded.
func events(t *testing.T, r Routes, userID string, request searchRequest) []map[string]any {
	t.Helper()
	var out []map[string]any
	r.run(context.Background(), userID, request, func(event any) bool {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		out = append(out, decoded)
		return true
	})
	return out
}

// kinds is the events as "type" or "type:detail", for comparing a sequence.
func kinds(list []map[string]any) string {
	var names []string
	for _, event := range list {
		name := event["type"].(string)
		switch name {
		case "phase":
			name += ":" + event["phase"].(string)
		case "provider":
			name += ":" + event["source"].(string) + ":" + event["status"].(string)
		case "error":
			name += ":" + event["code"].(string)
		}
		names = append(names, name)
	}
	return strings.Join(names, " ")
}

func results(t *testing.T, list []map[string]any) []map[string]any {
	t.Helper()
	for _, event := range list {
		if event["type"] == "results" {
			var rows []map[string]any
			for _, row := range event["results"].([]any) {
				rows = append(rows, row.(map[string]any))
			}
			return rows
		}
	}
	t.Fatalf("no results event in %s", kinds(list))
	return nil
}

func TestAPubMedSearchIsProgressThenItsPapersThenDone(t *testing.T) {
	sources := &papers{pubmed: []academic.Paper{tidesPaper, seaPaper}}
	r := Routes{Papers: sources, Web: web{}, Pages: &pages{}, Logger: quiet()}
	got := events(t, r, reader, searchRequest{Query: "tides", Sources: []string{"pubmed"}})
	if want := "phase:searching phase:searching phase:ranking results done"; kinds(got) != want {
		t.Fatalf("events = %s, want %s", kinds(got), want)
	}
	rows := results(t, got)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	first := rows[0]
	if first["source"] != "pubmed" || first["title"] != "Tides and the moon" || first["snippet"] != "Tides follow the moon." ||
		first["meta"] != "A One, B Two, C Three et al. · Journal of Tides · 2024" || len(first["id"].(string)) != 40 {
		t.Errorf("first row = %v", first)
	}
	paper := first["paper"].(map[string]any)
	if paper["pmid"] != "111" || paper["pmcid"] != "PMC900" || paper["open_access"] != true || paper["year"] != float64(2024) ||
		len(paper["badges"].([]any)) != 2 {
		t.Errorf("first row's paper = %v", paper)
	}
	// The apps' schema names both; this tier has no image proxy.
	if _, named := first["thumbnail_url"]; !named || first["thumbnail_url"] != nil {
		t.Errorf("thumbnail_url = %v, want a null that is present", first["thumbnail_url"])
	}
	// A paper outside PubMed Central is not open and has one badge.
	second := rows[1]["paper"].(map[string]any)
	if second["open_access"] != false || len(second["badges"].([]any)) != 1 {
		t.Errorf("second row's paper = %v", second)
	}
}

// The sources are asked separately and reported separately; the rows are
// listed in turn and each names the source that found it.
func TestSeveralSourcesAreAskedSeparatelyAndNeverMerged(t *testing.T) {
	sources := &papers{pubmed: []academic.Paper{tidesPaper, seaPaper}, arxiv: []academic.Paper{arxivPaper}, preprints: []academic.Paper{bioPaper}}
	r := Routes{Papers: sources, Web: web{}, Pages: &pages{}, Logger: quiet()}
	got := events(t, r, reader, searchRequest{Query: "tides", Sources: []string{"pubmed", "arxiv", "medrxiv", "exa"}})

	text := kinds(got)
	for _, piece := range []string{
		"provider:pubmed:searching provider:arxiv:searching provider:medrxiv:searching provider:exa:searching",
		"provider:pubmed:done", "provider:arxiv:done", "provider:medrxiv:done", "provider:exa:error",
	} {
		if !strings.Contains(text, piece) {
			t.Errorf("events %s lack %s", text, piece)
		}
	}
	if !strings.HasSuffix(text, "phase:ranking results done") {
		t.Errorf("events end %s", text)
	}
	for _, event := range got {
		if event["type"] == "provider" && event["source"] == "exa" && event["status"] == "error" && event["code"] != "source_not_connected" {
			t.Errorf("Exa, not connected, failed as %v", event["code"])
		}
		if event["type"] == "provider" && event["source"] == "pubmed" && event["status"] == "done" && event["count"] != float64(2) {
			t.Errorf("PubMed's count = %v", event["count"])
		}
	}
	var order []string
	for _, row := range results(t, got) {
		order = append(order, row["source"].(string))
	}
	// The first of each, then the second of each.
	if strings.Join(order, " ") != "pubmed arxiv medrxiv pubmed" {
		t.Errorf("rows by source = %v", order)
	}
	// The preprint search asks both servers, and the row says which one
	// the paper is on.
	sources.mu.Lock()
	asked := strings.Join(sources.asked, " ")
	sources.mu.Unlock()
	if !strings.Contains(asked, "preprints:") || strings.Contains(asked, "preprints:medrxiv") {
		t.Errorf("asked %s; the preprint search should name no single server", asked)
	}
	for _, row := range results(t, got) {
		if row["source"] == "medrxiv" && row["paper"].(map[string]any)["server"] != "biorxiv" {
			t.Errorf("a bioRxiv preprint's server = %v", row["paper"].(map[string]any)["server"])
		}
	}
}

func TestWhyASourceGaveNothing(t *testing.T) {
	both := map[string]map[websearch.Provider]bool{reader: {websearch.Exa: true, websearch.Tavily: true}}
	for name, c := range map[string]struct {
		routes  Routes
		source  string
		code    string
		message string
	}{
		"PubMed is down":          {Routes{Papers: &papers{failing: map[string]error{"pubmed": errUnreachable}}, Web: web{}}, "pubmed", "source_busy", "PubMed didn't answer"},
		"PubMed runs out of time": {Routes{Papers: &papers{failing: map[string]error{"pubmed": context.DeadlineExceeded}}, Web: web{}}, "pubmed", "source_timeout", "too long"},
		"Exa is not connected":    {Routes{Papers: &papers{}, Web: web{}}, "exa", "source_not_connected", "Exa isn't connected"},
		"Tavily refuses the key":  {Routes{Papers: &papers{}, Web: web{connected: both, failing: map[websearch.Provider]error{websearch.Tavily: websearch.ErrKeyRejected}}}, "tavily", "source_key_rejected", "Tavily refused"},
		"Exa is out of quota":     {Routes{Papers: &papers{}, Web: web{connected: both, failing: map[websearch.Provider]error{websearch.Exa: websearch.ErrQuota}}}, "exa", "source_quota", "out of credit"},
		"Scholar is not searched": {Routes{Papers: &papers{}, Web: web{}}, "google_scholar", "source_unavailable", "Google Scholar can't be searched on this server"},
	} {
		c.routes.Pages, c.routes.Logger = &pages{}, quiet()
		got := events(t, c.routes, reader, searchRequest{Query: "tides", Sources: []string{c.source}})
		last := got[len(got)-1]
		if last["type"] != "error" || last["code"] != c.code || !strings.Contains(last["message"].(string), c.message) {
			t.Errorf("%s: last event = %v", name, last)
		}
		// One source: the failure is the search's, and names no source.
		if _, named := last["source"]; named {
			t.Errorf("%s: a single-source failure names a source", name)
		}
		// The cause is logged, never sent: it can name an address.
		if strings.Contains(last["message"].(string), "eutils") {
			t.Errorf("%s: the message carries the cause: %v", name, last["message"])
		}
	}
}

// One source failing does not fail the search; all of them failing does,
// worded after the first one picked.
func TestASearchFailsOnlyWhenEverySourceDoes(t *testing.T) {
	down := map[string]error{"pubmed": errUnreachable}
	r := Routes{Papers: &papers{failing: down, arxiv: []academic.Paper{arxivPaper}}, Web: web{}, Pages: &pages{}, Logger: quiet()}
	got := events(t, r, reader, searchRequest{Query: "tides", Sources: []string{"pubmed", "arxiv"}})
	if rows := results(t, got); len(rows) != 1 || rows[0]["source"] != "arxiv" {
		t.Errorf("rows = %v", rows)
	}

	got = events(t, r, reader, searchRequest{Query: "tides", Sources: []string{"pubmed", "exa"}})
	last := got[len(got)-1]
	if last["type"] != "error" || last["code"] != "source_busy" || last["source"] != "pubmed" {
		t.Errorf("last event = %v", last)
	}
}

// A deep search quotes the paper's open full text and the web page itself.
func TestADeepSearchQuotesWhatItRead(t *testing.T) {
	body := strings.Repeat("Preamble words. ", 60) + "The spring tides are highest at full moon. " + strings.Repeat("More words. ", 60)
	sources := &papers{pubmed: []academic.Paper{tidesPaper, seaPaper}, fullText: map[string]string{"PMC900": body}}
	got := events(t, Routes{Papers: sources, Web: web{}, Pages: &pages{}, Logger: quiet()}, reader,
		searchRequest{Query: "spring tides", Sources: []string{"pubmed"}, Deep: true})
	rows := results(t, got)
	if snippet := rows[0]["snippet"].(string); !strings.Contains(snippet, "spring tides are highest") || !strings.HasPrefix(snippet, "…") {
		t.Errorf("the open paper's snippet = %q", snippet)
	}
	if rows[1]["snippet"] != "Levels rise." {
		t.Errorf("a paper with no open text lost its abstract: %v", rows[1]["snippet"])
	}
	// No web result, so nothing is read and no reading phase is claimed.
	if strings.Contains(kinds(got), "phase:reading") {
		t.Errorf("events = %s", kinds(got))
	}

	connected := map[string]map[websearch.Provider]bool{reader: {websearch.Exa: true}}
	found := []websearch.Result{
		{Title: "Tide tables", URL: "https://tides.example/tables", Snippet: "A teaser.", Meta: "tides.example"},
		{Title: "Gone", URL: "https://gone.example/", Snippet: "Its teaser."},
	}
	reads := &pages{byAddress: map[string]pagereader.Page{
		"https://tides.example/tables": {Status: pagereader.Read, Text: "Intro. The spring tides table lists heights."},
	}}
	r := Routes{Papers: &papers{}, Web: web{connected: connected, results: map[websearch.Provider][]websearch.Result{websearch.Exa: found}}, Pages: reads, Logger: quiet()}
	got = events(t, r, reader, searchRequest{Query: "spring tides", Sources: []string{"exa"}, Deep: true})
	if !strings.Contains(kinds(got), "phase:reading phase:reading phase:ranking") {
		t.Errorf("events = %s", kinds(got))
	}
	rows = results(t, got)
	if !strings.Contains(rows[0]["snippet"].(string), "spring tides table") {
		t.Errorf("the read page's snippet = %v", rows[0]["snippet"])
	}
	// A page that could not be read keeps the search's own teaser.
	if rows[1]["snippet"] != "Its teaser." || rows[0]["meta"] != "tides.example" || rows[1]["meta"] != nil {
		t.Errorf("rows = %v", rows)
	}
	// A fast search reads nothing.
	reads.read = nil
	events(t, r, reader, searchRequest{Query: "spring tides", Sources: []string{"exa"}})
	if len(reads.read) != 0 {
		t.Errorf("a fast search read %v", reads.read)
	}
}

// A slow source still sends progress, and a reader who goes away stops it.
func TestAQuietSearchKeepsReportingAndStopsWithItsReader(t *testing.T) {
	sources := &papers{pubmed: []academic.Paper{tidesPaper}, hold: make(chan struct{})}
	r := Routes{Papers: sources, Web: web{}, Pages: &pages{}, Logger: quiet(), keepAlive: 5 * time.Millisecond}
	ctx, gone := context.WithCancel(context.Background())
	seen := 0
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r.run(ctx, reader, searchRequest{Query: "tides", Sources: []string{"pubmed"}}, func(event any) bool {
			seen++
			if seen == 4 {
				// Three keep-alives after the first event: the reader leaves.
				gone()
				return false
			}
			return true
		})
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the search went on after its reader had gone")
	}
	if seen != 4 {
		t.Errorf("events seen = %d", seen)
	}
}

// A source that does not answer in its time is dropped, by name.
func TestASourceSlowerThanItsLimitIsDropped(t *testing.T) {
	sources := &papers{hold: make(chan struct{}), arxiv: []academic.Paper{arxivPaper}}
	r := Routes{Papers: sources, Web: web{}, Pages: &pages{}, Logger: quiet(), sourceTimeout: 20 * time.Millisecond}
	got := events(t, r, reader, searchRequest{Query: "tides", Sources: []string{"pubmed", "arxiv"}})
	for _, event := range got {
		if event["type"] == "provider" && event["source"] == "pubmed" && event["status"] == "error" {
			if event["code"] != "source_timeout" {
				t.Errorf("the slow source failed as %v", event["code"])
			}
			if rows := results(t, got); len(rows) != 1 {
				t.Errorf("rows = %v", rows)
			}
			return
		}
	}
	t.Errorf("the slow source was never reported: %s", kinds(got))
}

func TestWhatASearchRequestMaySay(t *testing.T) {
	for name, c := range map[string]struct {
		body    string
		sources string
		deep    bool
		field   string
	}{
		"one source":            {`{"query":" tides ","source":"pubmed"}`, "pubmed", false, ""},
		"several, deep":         {`{"query":"t","source":"pubmed","sources":["pubmed","arxiv"],"mode":"deep"}`, "pubmed arxiv", true, ""},
		"a source named twice":  {`{"query":"t","sources":["arxiv","arxiv","exa"]}`, "arxiv exa", false, ""},
		"Scholar is known":      {`{"query":"t","source":"google_scholar"}`, "google_scholar", false, ""},
		"no source at all":      {`{"query":"t"}`, "", false, "sources"},
		"a source that was cut": {`{"query":"t","source":"reddit"}`, "", false, "source"},
		"five sources":          {`{"query":"t","sources":["exa","tavily","pubmed","arxiv","medrxiv"]}`, "", false, "sources"},
		"an empty list":         {`{"query":"t","sources":[]}`, "", false, "sources"},
		"a blank query":         {`{"query":"   ","source":"pubmed"}`, "", false, "query"},
		"a query too long":      {`{"query":"` + strings.Repeat("q", 501) + `","source":"pubmed"}`, "", false, "query"},
		"an unknown mode":       {`{"query":"t","source":"pubmed","mode":"thorough"}`, "", false, "mode"},
		"not an object":         {`[1]`, "", false, "body"},
	} {
		request, location, problem := readSearch([]byte(c.body))
		if c.field != "" {
			if problem == "" || location[len(location)-1] != c.field {
				t.Errorf("%s: refused at %v (%q), want %s", name, location, problem, c.field)
			}
			continue
		}
		if problem != "" || strings.Join(request.Sources, " ") != c.sources || request.Deep != c.deep || request.Query != strings.TrimSpace(request.Query) {
			t.Errorf("%s: request = %+v, problem %q", name, request, problem)
		}
	}
}
