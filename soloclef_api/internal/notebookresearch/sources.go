package notebookresearch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// The sources, by the ids both apps send. The order is the order they are
// reported in.
const (
	sourceExa      = "exa"
	sourceTavily   = "tavily"
	sourcePubMed   = "pubmed"
	sourceArxiv    = "arxiv"
	sourcePreprint = "medrxiv"
	// sourceScholar is known and not searched: see the package comment.
	sourceScholar = "google_scholar"
)

// sourceNames is every source a request may name, with the name a sentence
// uses for it. A name not here is a 422.
var sourceNames = map[string]string{
	sourceExa: "Exa", sourceTavily: "Tavily", sourcePubMed: "PubMed", sourceArxiv: "arXiv",
	sourcePreprint: "medRxiv / bioRxiv", sourceScholar: "Google Scholar",
}

// webProviders are the sources answered with the account's own key.
var webProviders = map[string]websearch.Provider{sourceExa: websearch.Exa, sourceTavily: websearch.Tavily}

// How many results a search asks one source for. A deep search asks for
// more. The keyed web services answer at most ten per request, which is the
// chat's limit for them too (websearch), and a second request would spend
// the account's quota twice for one search.
const (
	fastLimit = 10
	deepLimit = 25
)

// snippetChars is how much of a result's text a row carries.
const snippetChars = 400

// Row is one result a person can tick and import.
type Row struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Source  string `json:"source"`
	// Both are always null: the apps only ever load a result's image from
	// this server, and this tier has no image proxy. They are in the row
	// because the apps' schema names them.
	ThumbnailURL *string `json:"thumbnail_url"`
	FaviconURL   *string `json:"favicon_url"`
	Meta         *string `json:"meta"`
	Paper        *Paper  `json:"paper"`
	// fullText is a paper's open body, when a deep search fetched it.
	fullText string
}

// failure is why one source gave no rows: a code the apps word in the
// reader's language, and a sentence for a client that does not know it.
type failure struct {
	Code    string
	Message string
}

func busy(source string) *failure {
	return &failure{"source_busy", sourceNames[source] + " didn't answer just now. Try again in a minute."}
}

// find asks one source and returns its rows, or why there are none. It is
// the one place a source's own errors are turned into the box's codes.
func (r Routes) find(ctx context.Context, userID, source, query string, deep bool) ([]Row, *failure) {
	limit := fastLimit
	if deep {
		limit = deepLimit
	}
	if provider, web := webProviders[source]; web {
		results, err := r.Web.Find(ctx, userID, provider, query, limit)
		switch {
		case err == nil:
			return webRows(results, source), nil
		case errors.Is(err, websearch.ErrNotConnected):
			return nil, &failure{"source_not_connected", sourceNames[source] + " isn't connected. Add its connector in Settings, then search again."}
		case errors.Is(err, websearch.ErrKeyRejected):
			return nil, &failure{"source_key_rejected", sourceNames[source] + " refused your API key. Check its connector."}
		case errors.Is(err, websearch.ErrQuota):
			return nil, &failure{"source_quota", sourceNames[source] + " turned the search down: the key is out of credit or over its rate limit."}
		}
		return nil, r.unanswered(ctx, source, err)
	}
	var (
		papers []academic.Paper
		err    error
	)
	switch source {
	case sourcePubMed:
		// A deep search also reads the open full text of the top papers,
		// so their snippet quotes the paper and not its abstract's opening.
		papers, err = r.Papers.PubMed(ctx, query, limit, deep, 0, 0)
	case sourceArxiv:
		papers, err = r.Papers.Arxiv(ctx, query, limit)
	case sourcePreprint:
		// Both servers: the source is "medRxiv / bioRxiv" in the menu.
		papers, err = r.Papers.Preprints(ctx, query, "", limit)
	case sourceScholar:
		return nil, &failure{"source_unavailable", "Google Scholar can't be searched on this server. Search PubMed, arXiv or medRxiv / bioRxiv instead."}
	default:
		// Requests are held to sourceNames before they reach here.
		return nil, &failure{"search_failed", searchFailed}
	}
	if err != nil {
		return nil, r.unanswered(ctx, source, err)
	}
	return paperRows(papers, source, query), nil
}

// unanswered is a source that could not be reached or answered something
// unreadable. The cause is logged and not sent: it can name an address.
func (r Routes) unanswered(ctx context.Context, source string, err error) *failure {
	if errors.Is(err, context.DeadlineExceeded) {
		return &failure{"source_timeout", "This source took too long to answer."}
	}
	if !errors.Is(err, context.Canceled) {
		r.Logger.WarnContext(ctx, "a research source did not answer", "source", source, "error", err.Error())
	}
	return busy(source)
}

func webRows(results []websearch.Result, source string) []Row {
	rows := make([]Row, 0, len(results))
	for _, result := range results {
		address, text, ok := webAddress(result.URL)
		if !ok {
			continue
		}
		row := Row{ID: resultID(address), URL: text, Title: result.Title, Snippet: trim(result.Snippet, snippetChars), Source: source}
		if result.Meta != "" {
			meta := result.Meta
			row.Meta = &meta
		}
		rows = append(rows, row)
	}
	return rows
}

func paperRows(papers []academic.Paper, source, query string) []Row {
	rows := make([]Row, 0, len(papers))
	for _, paper := range papers {
		address, text, ok := webAddress(paper.URL)
		if !ok {
			continue
		}
		row := Row{ID: resultID(address), URL: text, Title: paper.Title, Source: source,
			Snippet: trim(paper.Abstract, snippetChars), Paper: paperOf(paper, source), fullText: paper.FullText}
		if paper.FullText != "" {
			row.Snippet = excerpt(paper.FullText, query, snippetChars)
		}
		if meta := byline(paper); meta != "" {
			row.Meta = &meta
		}
		rows = append(rows, row)
	}
	return rows
}

// byline is "first three authors et al. · venue · year".
func byline(paper academic.Paper) string {
	var parts []string
	if len(paper.Authors) > 3 {
		parts = append(parts, strings.Join(paper.Authors[:3], ", ")+" et al.")
	} else if len(paper.Authors) > 0 {
		parts = append(parts, strings.Join(paper.Authors, ", "))
	}
	for _, part := range []string{paper.Venue, paper.Year} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// trim is text on one line, cut to most characters with an ellipsis.
func trim(text string, most int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= most {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:most-1])) + "…"
}

// excerpt is the part of text that speaks to the query: the stretch around
// the first place one of the query's words appears, or the opening when
// none does. A word of fewer than three letters says too little to anchor
// on.
func excerpt(text, query string, most int) string {
	flat := []rune(strings.Join(strings.Fields(text), " "))
	lower := []rune(strings.ToLower(string(flat)))
	first := -1
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if utf8.RuneCountInString(word) < 3 {
			continue
		}
		if at := indexRunes(lower, []rune(word)); at >= 0 && (first < 0 || at < first) {
			first = at
		}
	}
	// A little before the word, so the sentence it is in is readable.
	start := max(first-most/4, 0)
	if first < 0 {
		start = 0
	}
	for start > 0 && start < len(flat) && flat[start-1] != ' ' {
		start++
	}
	end := min(start+most, len(flat))
	out := strings.TrimSpace(string(flat[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(flat) {
		out += "…"
	}
	return out
}

func indexRunes(text, word []rune) int {
	for i := 0; i+len(word) <= len(text); i++ {
		match := true
		for j := range word {
			if text[i+j] != word[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// sourcesStatus is what the box's menu reads before a search: which sources
// are greyed out, each with why, and what reads a pasted page.
type sourcesStatus struct {
	Unavailable map[string]string `json:"unavailable"`
	// PageReader is always "crawler": this tier reads a page itself. The
	// apps also know "browserbase", a rendering service this tier does not
	// call.
	PageReader string `json:"page_reader"`
}

// The reasons a source is greyed out.
const (
	// notConnected: the account has not connected the service's key.
	notConnected = "not_connected"
	// notAvailable: this server does not search the source at all.
	notAvailable = "not_available"
)

func (r Routes) sources(x *exchange) {
	if !x.owned() {
		return
	}
	status := sourcesStatus{Unavailable: map[string]string{sourceScholar: notAvailable}, PageReader: "crawler"}
	for _, source := range []string{sourceExa, sourceTavily} {
		// Only whether a key exists is asked, and only that is told.
		connected, err := r.Web.Connected(x.ctx.Context(), x.userID, webProviders[source])
		if err != nil {
			x.fail("could not read the account's web search connections", err)
			return
		}
		if !connected {
			status.Unavailable[source] = notConnected
		}
	}
	x.send(http.StatusOK, status)
}
