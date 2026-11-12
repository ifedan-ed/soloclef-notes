// Package academicsearch serves one academic source's search on its own,
// with no notebook and no import:
//
//	POST /api/v1/academic-search/pubmed    PubMed, optionally a year window and open full text
//	POST /api/v1/academic-search/arxiv     arXiv
//	POST /api/v1/academic-search/medrxiv   medRxiv and bioRxiv; `server` keeps one of them
//
// They are what the MCP server's soloclef_pubmed_search, soloclef_arxiv_search
// and soloclef_preprint_search tools call. Each source is its own route and
// its own search: the caller picks the source, and nothing here merges two or
// chooses between them.
//
// The searches themselves are the chat's (soloclef_assistant/academic), the very
// value the chat's tools run on, so the spacing between requests to one host
// holds across the chat and these routes together.
//
// # Google Scholar
//
// There is no `…/google_scholar` route. Scholar has no API; the Python tier
// scraped its result pages, which Google's terms forbid and its wall soon
// blocks, and nothing in this tier does that. A route that always refused
// would keep a tool on the MCP surface that can never answer, so the path is
// simply not one of this tier's and the MCP tool is gone with it.
//
// # What it does better than the Python
//
//   - **The preprint server is asked for, not filtered afterwards.** The
//     Python searched both servers, threw away the other one's rows and so
//     had to fetch a deep pool to have enough left. Here `server` is part of
//     the index query, and `max_results` is what is fetched.
//   - **Recent preprints are found.** The server is the one the index names,
//     not a 10.1101/ DOI prefix both servers stopped issuing in 2025.
//   - **A source that is down is a 502 that names it**, not a 500.
//   - **A search is bounded**: forty-five seconds, inside the server's write
//     timeout, so a slow source is an answer and not a cut connection.
package academicsearch

import (
	"net/http"
	"time"
)

// The paths. Three literals, not one `{source}` pattern: a source is a
// route, and a name that is not one is the router's 404.
const (
	PubMedPath   = "/api/v1/academic-search/pubmed"
	ArxivPath    = "/api/v1/academic-search/arxiv"
	PreprintPath = "/api/v1/academic-search/medrxiv"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodPost + " " + PubMedPath,
		http.MethodPost + " " + ArxivPath,
		http.MethodPost + " " + PreprintPath,
	}
}

const (
	// The request's bounds, which are also the MCP tools' published ones.
	maxQueryChars  = 500
	defaultResults = 10
	maxResults     = 30
	minYear        = 1000
	maxYear        = 3000
	// A snippet is a few lines under a title, not the abstract.
	maxSnippetChars = 400
	// A search may take this long. arXiv alone asks for three seconds
	// between requests, and a busy source is retried twice.
	searchTimeout = 45 * time.Second
	// The body is a query and five small fields.
	maxBodyBytes = 16 << 10
)
