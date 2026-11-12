package academicsearch

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
)

// row is one result as the MCP tools and the research box read it. The
// field names are the contract; thumbnail_url and favicon_url are part of the
// shape and always null, because a paper has neither.
type row struct {
	ID        string  `json:"id"`
	URL       string  `json:"url"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	Source    string  `json:"source"`
	Thumbnail *string `json:"thumbnail_url"`
	Favicon   *string `json:"favicon_url"`
	Meta      *string `json:"meta"`
	Paper     paper   `json:"paper"`
}

// paper is a result's scholarly identity: the ids a paper can be found
// again by, whichever source it came from.
type paper struct {
	DOI        *string  `json:"doi"`
	PMID       *string  `json:"pmid"`
	PMCID      *string  `json:"pmcid"`
	ArxivID    *string  `json:"arxiv_id"`
	Server     *string  `json:"server"`
	Authors    []string `json:"authors"`
	Venue      *string  `json:"venue"`
	Year       *int     `json:"year"`
	Badges     []string `json:"badges"`
	OpenAccess bool     `json:"open_access"`
	// FullText says the snippet is an excerpt of the paper's body, not of
	// its abstract.
	FullText bool `json:"full_text"`
}

// The addresses the sources give their papers (soloclef_assistant/academic).
// The ids are read back out of them: the address is built from the id by
// the source's own reader, so this is the id and not a guess at it.
const (
	pubmedAddress = "https://pubmed.ncbi.nlm.nih.gov/"
	arxivAddress  = "https://arxiv.org/abs/"
	doiAddress    = "https://doi.org/"
)

// rowsOf is the papers as rows of one source.
func rowsOf(source string, papers []academic.Paper) []row {
	rows := make([]row, 0, len(papers))
	for _, p := range papers {
		rows = append(rows, rowOf(source, p))
	}
	return rows
}

func rowOf(source string, p academic.Paper) row {
	out := row{
		ID: source + ":" + p.URL, URL: p.URL, Title: p.Title, Source: source,
		Snippet: clipped(p.Abstract),
		Paper:   paper{Authors: p.Authors, Badges: []string{}},
	}
	if out.Paper.Authors == nil {
		out.Paper.Authors = []string{}
	}
	if meta := p.Meta(); meta != "" {
		out.Meta = &meta
	}
	if p.Venue != "" {
		out.Paper.Venue = &p.Venue
	}
	if year, err := strconv.Atoi(p.Year); err == nil {
		out.Paper.Year = &year
	}
	if p.FullText != "" {
		out.Snippet, out.Paper.FullText = clipped(p.FullText), true
	}
	badge := func(name string) { out.Paper.Badges = append(out.Paper.Badges, name) }
	switch source {
	case "pubmed":
		if id := strings.Trim(strings.TrimPrefix(p.URL, pubmedAddress), "/"); id != "" && id != p.URL {
			out.Paper.PMID = &id
		}
		badge("pubmed")
		if p.PMCID != "" {
			// In PubMed Central, which is where open full text is read
			// from.
			out.Paper.PMCID, out.Paper.OpenAccess = &p.PMCID, true
			badge("pmc")
		}
	case "arxiv":
		if id, found := strings.CutPrefix(p.URL, arxivAddress); found {
			out.Paper.ArxivID = &id
		}
		out.Paper.OpenAccess = true
		badge("arxiv")
	case "medrxiv":
		if doi, found := strings.CutPrefix(p.URL, doiAddress); found {
			out.Paper.DOI = &doi
		}
		// The venue is the server as the index named it, "medRxiv
		// (preprint)": never inferred from the DOI.
		if name, _, found := strings.Cut(p.Venue, " "); found {
			server := strings.ToLower(name)
			out.Paper.Server = &server
			badge(server)
		}
		out.Paper.OpenAccess = true
	}
	return out
}

// clipped is text cut to a snippet on a rune boundary.
func clipped(text string) string {
	if utf8.RuneCountInString(text) <= maxSnippetChars {
		return text
	}
	return string([]rune(text)[:maxSnippetChars]) + "…"
}
