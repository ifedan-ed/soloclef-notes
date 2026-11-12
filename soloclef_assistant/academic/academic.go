// Package academic is the chat's scholarly sources: PubMed (with PubMed
// Central's open full text), arXiv, and the bioRxiv and medRxiv preprint
// servers.
//
// Each source is its own tool. The person picks the source a message uses,
// and the sources are different things to pick between: peer-reviewed
// medicine, physics and computing preprints, life-science preprints. They
// are never merged into one "science search" that chooses for the reader.
//
// These sources were not in chat before; they were reachable only from the
// notebook research box. Everything here is a public, keyless API (an NCBI
// key is optional and only raises PubMed's rate limit), so the tools are
// offered to every account.
//
// # What this does better than the research box's readers
//
//   - Preprints are recognised by the server Europe PMC names, not by a DOI
//     prefix. bioRxiv and medRxiv stopped issuing 10.1101/ DOIs in 2025;
//     a reader that keeps only that prefix finds no recent preprint at all.
//   - Each paper is its own cited source, by its address.
//   - Requests to one host are spaced across the whole process and honour
//     the request's cancellation, so a stopped turn stops waiting.
package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

// The tools' names, one per source.
const (
	PubMedTool   = "pubmed_search"
	ArxivTool    = "arxiv_search"
	PreprintTool = "preprint_search"
)

// The sources' endpoints.
const (
	eutilsBase    = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils"
	arxivBase     = "https://export.arxiv.org/api/query"
	europePMCBase = "https://www.ebi.ac.uk/europepmc/webservices/rest/search"
)

// maxPapers is the most papers one search returns; defaultPapers is how many
// it returns when the model does not say.
const (
	maxPapers     = 20
	defaultPapers = 8
)

// fullTextPapers is how many of a PubMed search's papers have their full
// text fetched when it is asked for, and fullTextChars how much of each is
// shown. Full text is one more request per paper and a great deal of text;
// three papers' worth is what fits beside an answer.
const (
	fullTextPapers = 3
	fullTextChars  = 6000
)

// userAgent identifies the caller to the sources, as their terms ask. It
// names no deployment and no person.
const userAgent = "Soloclef/1.0 (chat research)"

// Settings is what the sources need. NCBI asks every caller to name its
// tool and, optionally, to send an address and a key.
type Settings struct {
	HTTP *http.Client
	// NCBITool is the "tool" parameter NCBI asks for. Required.
	NCBITool string
	// NCBIEmail and NCBIAPIKey are optional: NCBI works without them, at
	// three requests a second instead of ten.
	NCBIEmail  string
	NCBIAPIKey string
}

// Sources runs the searches. One value serves the whole process, because
// the spacing between requests to a host is a property of the process.
type Sources struct {
	settings Settings
	pace     *pacer
	// bases are the endpoints, replaced by tests.
	eutils, arxiv, europePMC string
	// retryPause is how long to wait before retry n when the source gave no
	// Retry-After. A field so that a test does not sleep for seconds.
	retryPause func(attempt int) time.Duration
}

// New checks settings and returns the sources.
func New(settings Settings) (*Sources, error) {
	switch {
	case settings.HTTP == nil:
		return nil, errors.New("academic: the HTTP client is not set")
	case strings.TrimSpace(settings.NCBITool) == "":
		return nil, errors.New("academic: NCBITool is not set; NCBI asks every caller to name itself")
	}
	ncbiGap := 340 * time.Millisecond
	if settings.NCBIAPIKey != "" {
		ncbiGap = 110 * time.Millisecond
	}
	return &Sources{
		settings: settings,
		// The gaps are the sources' published limits: NCBI 3 a second (10
		// with a key), arXiv one every 3 seconds, Europe PMC generous.
		pace: newPacer(map[string]time.Duration{
			"eutils.ncbi.nlm.nih.gov": ncbiGap,
			"export.arxiv.org":        3 * time.Second,
			"www.ebi.ac.uk":           200 * time.Millisecond,
		}),
		eutils: eutilsBase, arxiv: arxivBase, europePMC: europePMCBase,
		retryPause: func(attempt int) time.Duration { return time.Duration(attempt) * 3 * time.Second },
	}, nil
}

// Paper is one result, from whichever source.
type Paper struct {
	Title    string
	URL      string
	Authors  []string
	Venue    string
	Year     string
	Abstract string
	// PMCID is PubMed Central's id when the paper is there.
	PMCID string
	// FullText is the body's text, when it was asked for and is open.
	FullText string
}

// Render writes papers as the model reads them, numbering each in the
// thread's registry by its address.
func Render(papers []Paper, registry *citation.Registry) string {
	var b strings.Builder
	for i, p := range papers {
		if i > 0 {
			b.WriteString("\n\n")
		}
		n := registry.Number(citation.Source{Kind: cite.Web, Ref: p.URL})
		b.WriteString("[" + strconv.Itoa(n) + "] " + p.Title + "\n" + p.URL)
		if meta := p.meta(); meta != "" {
			b.WriteString("\n" + meta)
		}
		if p.Abstract != "" {
			b.WriteString("\nAbstract: " + p.Abstract)
		}
		if p.FullText != "" {
			b.WriteString("\nFull text (open access, may be cut short):\n" + p.FullText)
		}
	}
	return b.String()
}

// Meta is the one line under a paper's title, for a caller outside the chat
// that lists papers (internal/academicsearch): the same line the model reads.
func (p Paper) Meta() string { return p.meta() }

// meta is "first three authors et al. · venue · year".
func (p Paper) meta() string {
	var parts []string
	if len(p.Authors) > 3 {
		parts = append(parts, strings.Join(p.Authors[:3], ", ")+" et al.")
	} else if len(p.Authors) > 0 {
		parts = append(parts, strings.Join(p.Authors, ", "))
	}
	for _, part := range []string{p.Venue, p.Year} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// Tools returns the three tools. They read public data and send only the
// query, so none asks for approval; each may run beside other calls (the
// pacer keeps two calls to one host apart).
func (s *Sources) Tools() []tool.Tool {
	return []tool.Tool{
		{
			Name: PubMedTool,
			Description: "Search PubMed, the index of peer-reviewed biomedical and life-science literature. Returns papers labelled [n] with authors, journal, year and abstract. " +
				"Set full_text to also read the open-access full text of the top papers from PubMed Central, when a question needs methods or results that an abstract does not give. Cite a paper by writing its [n] after the claim.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"PubMed query: plain words, or PubMed syntax such as field tags and AND/OR."},"max_results":{"type":"integer","minimum":1,"maximum":20,"description":"How many papers. Default 8."},"full_text":{"type":"boolean","description":"Also fetch open-access full text for the top papers. Default false."},"since_year":{"type":"integer","description":"Only papers published in or after this year."},"until_year":{"type":"integer","description":"Only papers published in or before this year."}},"required":["query"]}`),
			Approval:   tool.Auto, Parallel: true,
			Run: s.run(func(ctx context.Context, a arguments) ([]Paper, error) {
				return s.PubMed(ctx, a.Query, a.limit(), a.FullText, a.SinceYear, a.UntilYear)
			}, "PubMed"),
		},
		{
			Name:        ArxivTool,
			Description: "Search arXiv, the preprint server for physics, mathematics, computer science, statistics and related fields. Returns preprints labelled [n] with authors, date and abstract. These are not peer reviewed; say so when it matters. Cite a paper by writing its [n] after the claim.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"A few specific words; every word must match."},"max_results":{"type":"integer","minimum":1,"maximum":20,"description":"How many papers. Default 8."}},"required":["query"]}`),
			Approval:    tool.Auto, Parallel: true,
			Run: s.run(func(ctx context.Context, a arguments) ([]Paper, error) {
				return s.Arxiv(ctx, a.Query, a.limit())
			}, "arXiv"),
		},
		{
			Name:        PreprintTool,
			Description: "Search the bioRxiv and medRxiv preprint servers (biology and health sciences). Returns preprints labelled [n] with authors, server, date and abstract. These are not peer reviewed; say so, especially for clinical claims. Cite a paper by writing its [n] after the claim.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Plain words describing the topic."},"server":{"type":"string","enum":["biorxiv","medrxiv"],"description":"Search only this server. Leave out to search both."},"max_results":{"type":"integer","minimum":1,"maximum":20,"description":"How many papers. Default 8."}},"required":["query"]}`),
			Approval:    tool.Auto, Parallel: true,
			Run: s.run(func(ctx context.Context, a arguments) ([]Paper, error) {
				return s.Preprints(ctx, a.Query, a.Server, a.limit())
			}, "The preprint servers"),
		},
	}
}

type arguments struct {
	Query      string `json:"query"`
	MaxResults *int   `json:"max_results"`
	FullText   bool   `json:"full_text"`
	SinceYear  int    `json:"since_year"`
	UntilYear  int    `json:"until_year"`
	Server     string `json:"server"`
}

func (a arguments) limit() int {
	if a.MaxResults == nil {
		return defaultPapers
	}
	return min(max(*a.MaxResults, 1), maxPapers)
}

// run wraps a source's search as a tool body: read the arguments, search,
// render, and word the two outcomes that are not results.
func (s *Sources) run(search func(context.Context, arguments) ([]Paper, error), name string) tool.Func {
	return func(ctx context.Context, call tool.Call) (string, error) {
		var args arguments
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return "", fmt.Errorf("the arguments are not readable: %w", err)
		}
		args.Query = strings.Join(strings.Fields(args.Query), " ")
		if args.Query == "" || len([]rune(args.Query)) > 500 {
			return "", errors.New("the query must be 1 to 500 characters")
		}
		papers, err := search(ctx, args)
		if err != nil {
			return "", err
		}
		if len(papers) == 0 {
			return fmt.Sprintf("%s found nothing for %q. Try fewer or different words.", name, args.Query), nil
		}
		return Render(papers, call.Citations), nil
	}
}

// get fetches one page from a source, spaced from the last request to that
// host, retrying twice when the source says it is busy.
func (s *Sources) get(ctx context.Context, address string, query url.Values) ([]byte, error) {
	target, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	target.RawQuery = query.Encode()
	const attempts = 3
	for attempt := 1; ; attempt++ {
		if err := s.pace.wait(ctx, target.Hostname()); err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", userAgent)
		response, err := s.settings.HTTP.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%s could not be reached just now", target.Hostname())
		}
		// Bounded: an abstract page is kilobytes and a full text a few
		// megabytes at most.
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		response.Body.Close()
		busy := response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusNotAcceptable || response.StatusCode >= 500
		switch {
		case response.StatusCode == http.StatusOK && readErr == nil:
			return body, nil
		case busy && attempt < attempts:
			pause := s.retryPause(attempt)
			if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 0 {
				// A source may ask for minutes; a chat turn will not wait.
				pause = min(time.Duration(seconds)*time.Second, 30*time.Second)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(pause):
			}
		default:
			return nil, fmt.Errorf("%s answered %d", target.Hostname(), response.StatusCode)
		}
	}
}

// pacer keeps requests to one host a minimum gap apart, across every turn
// in the process.
type pacer struct {
	gaps map[string]time.Duration
	mu   sync.Mutex
	next map[string]time.Time
}

func newPacer(gaps map[string]time.Duration) *pacer {
	return &pacer{gaps: gaps, next: map[string]time.Time{}}
}

// wait blocks until this caller's turn for host, or ctx ends. Each caller
// reserves the next slot before sleeping, so ten callers arriving together
// are spread out in arrival order instead of all waking at once.
func (p *pacer) wait(ctx context.Context, host string) error {
	gap := p.gaps[host]
	if gap == 0 {
		return ctx.Err()
	}
	p.mu.Lock()
	now := time.Now()
	slot := p.next[host]
	if slot.Before(now) {
		slot = now
	}
	p.next[host] = slot.Add(gap)
	p.mu.Unlock()
	if delay := slot.Sub(now); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}
