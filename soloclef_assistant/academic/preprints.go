package academic

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Arxiv searches arXiv. Every word of the query must match, which is what a
// reader typing three words expects; if that finds nothing, the same words
// are tried as alternatives, because a four-word query that matches no
// paper exactly usually has one word too many.
func (s *Sources) Arxiv(ctx context.Context, query string, limit int) ([]Paper, error) {
	words := strings.Fields(query)
	// arXiv's query language has its own operators; eight plain words is as
	// much as a relevance search uses.
	if len(words) > 8 {
		words = words[:8]
	}
	terms := make([]string, len(words))
	for i, word := range words {
		terms[i] = "all:" + strings.Trim(word, `"():`)
	}
	for _, joiner := range []string{" AND ", " OR "} {
		raw, err := s.get(ctx, s.arxiv, url.Values{
			"search_query": {strings.Join(terms, joiner)}, "start": {"0"},
			"max_results": {strconv.Itoa(limit)}, "sortBy": {"relevance"}, "sortOrder": {"descending"},
		})
		if err != nil {
			return nil, err
		}
		papers, err := parseArxiv(raw)
		if err != nil {
			return nil, err
		}
		if len(papers) > 0 || len(terms) < 2 {
			return papers, nil
		}
	}
	return nil, nil
}

func parseArxiv(raw []byte) ([]Paper, error) {
	var feed struct {
		Entries []struct {
			ID        string `xml:"id"`
			Title     string `xml:"title"`
			Summary   string `xml:"summary"`
			Published string `xml:"published"`
			Authors   []struct {
				Name string `xml:"name"`
			} `xml:"author"`
			Category struct {
				Term string `xml:"term,attr"`
			} `xml:"primary_category"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(raw, &feed); err != nil {
		return nil, fmt.Errorf("arXiv's answer is not readable")
	}
	papers := make([]Paper, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		// The id is the abstract page's address with the version: keep the
		// path and use https, so the citation opens the page.
		_, id, found := strings.Cut(entry.ID, "/abs/")
		if !found || strings.TrimSpace(entry.Title) == "" {
			continue
		}
		paper := Paper{
			Title:    strings.Join(strings.Fields(entry.Title), " "),
			URL:      "https://arxiv.org/abs/" + id,
			Venue:    "arXiv",
			Abstract: strings.Join(strings.Fields(entry.Summary), " "),
		}
		if entry.Category.Term != "" {
			paper.Venue = "arXiv " + entry.Category.Term
		}
		if len(entry.Published) >= 4 {
			paper.Year = entry.Published[:4]
		}
		for _, author := range entry.Authors {
			if name := strings.TrimSpace(author.Name); name != "" {
				paper.Authors = append(paper.Authors, name)
			}
		}
		papers = append(papers, paper)
	}
	return papers, nil
}

// servers are the preprint servers as Europe PMC names them.
var servers = map[string]string{"biorxiv": "bioRxiv", "medrxiv": "medRxiv"}

// Preprints searches bioRxiv and medRxiv through Europe PMC, which indexes
// both and, unlike the servers' own API, searches by words. server confines
// it to one of them; "" searches both.
func (s *Sources) Preprints(ctx context.Context, query, server string, limit int) ([]Paper, error) {
	publisher := `(PUBLISHER:"medRxiv" OR PUBLISHER:"bioRxiv")`
	if server != "" {
		name, known := servers[strings.ToLower(server)]
		if !known {
			return nil, fmt.Errorf("server must be biorxiv or medrxiv, not %q", server)
		}
		publisher = `PUBLISHER:"` + name + `"`
	}
	raw, err := s.get(ctx, s.europePMC, url.Values{
		"query":  {"(" + query + ") AND SRC:PPR AND " + publisher},
		"format": {"json"}, "pageSize": {strconv.Itoa(limit)}, "resultType": {"core"},
	})
	if err != nil {
		return nil, err
	}
	var answer struct {
		List struct {
			Results []struct {
				DOI      string `json:"doi"`
				Title    string `json:"title"`
				Authors  string `json:"authorString"`
				Abstract string `json:"abstractText"`
				Date     string `json:"firstPublicationDate"`
				Details  struct {
					Publisher string `json:"publisher"`
				} `json:"bookOrReportDetails"`
			} `json:"result"`
		} `json:"resultList"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("the preprint index's answer is not readable")
	}
	papers := make([]Paper, 0, len(answer.List.Results))
	for _, r := range answer.List.Results {
		// The server is the one the index names. It is not inferred from
		// the DOI: both servers changed prefix in 2025.
		name := ""
		for _, known := range servers {
			if strings.EqualFold(r.Details.Publisher, known) {
				name = known
			}
		}
		if name == "" || r.DOI == "" || strings.TrimSpace(r.Title) == "" {
			continue
		}
		paper := Paper{
			Title: flatten(r.Title),
			// doi.org resolves to whichever server holds the preprint.
			URL:      "https://doi.org/" + r.DOI,
			Venue:    name + " (preprint)",
			Abstract: flatten(r.Abstract),
		}
		if len(r.Date) >= 4 {
			paper.Year = r.Date[:4]
		}
		for _, author := range strings.Split(strings.TrimSuffix(strings.TrimSpace(r.Authors), "."), ",") {
			if author = strings.TrimSpace(author); author != "" {
				paper.Authors = append(paper.Authors, author)
			}
		}
		papers = append(papers, paper)
	}
	return papers, nil
}
