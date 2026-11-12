package academic

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ncbi returns the parameters NCBI asks every request to carry.
func (s *Sources) ncbi(values url.Values) url.Values {
	values.Set("tool", s.settings.NCBITool)
	if s.settings.NCBIEmail != "" {
		values.Set("email", s.settings.NCBIEmail)
	}
	if s.settings.NCBIAPIKey != "" {
		values.Set("api_key", s.settings.NCBIAPIKey)
	}
	return values
}

// PubMed searches PubMed and returns up to limit papers in relevance order.
// A year window, when given, is added in PubMed's own syntax. With fullText,
// the first few papers that are in PubMed Central have their body fetched.
func (s *Sources) PubMed(ctx context.Context, query string, limit int, fullText bool, sinceYear, untilYear int) ([]Paper, error) {
	if sinceYear > 0 || untilYear > 0 {
		low, high := "1000", "3000"
		if sinceYear > 0 {
			low = strconv.Itoa(sinceYear)
		}
		if untilYear > 0 {
			high = strconv.Itoa(untilYear)
		}
		query = fmt.Sprintf(`(%s) AND ("%s"[pdat] : "%s"[pdat])`, query, low, high)
	}
	// Two requests: the search answers ids, the fetch answers the records.
	raw, err := s.get(ctx, s.eutils+"/esearch.fcgi", s.ncbi(url.Values{
		"db": {"pubmed"}, "term": {query}, "retmax": {strconv.Itoa(limit)}, "sort": {"relevance"}, "retmode": {"json"},
	}))
	if err != nil {
		return nil, err
	}
	var found struct {
		Result struct {
			IDs []string `json:"idlist"`
		} `json:"esearchresult"`
	}
	if err := json.Unmarshal(raw, &found); err != nil {
		return nil, fmt.Errorf("PubMed's search answer is not readable")
	}
	if len(found.Result.IDs) == 0 {
		return nil, nil
	}
	raw, err = s.get(ctx, s.eutils+"/efetch.fcgi", s.ncbi(url.Values{
		"db": {"pubmed"}, "id": {strings.Join(found.Result.IDs, ",")}, "retmode": {"xml"},
	}))
	if err != nil {
		return nil, err
	}
	byID, err := parsePubMed(raw)
	if err != nil {
		return nil, err
	}
	// The fetch answers in its own order; the search's order is relevance.
	papers := make([]Paper, 0, len(found.Result.IDs))
	for _, id := range found.Result.IDs {
		if paper, ok := byID[id]; ok {
			papers = append(papers, paper)
		}
	}
	if fullText {
		fetched := 0
		for i := range papers {
			if fetched == fullTextPapers {
				break
			}
			if papers[i].PMCID == "" {
				continue
			}
			// A body that cannot be had (embargoed, not open, the fetch
			// failed) leaves the paper with its abstract. The search is
			// not failed for it; the model sees which papers have text.
			if body, err := s.pmcBody(ctx, papers[i].PMCID); err == nil && body != "" {
				papers[i].FullText = body
				fetched++
			} else if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}
	return papers, nil
}

// The parts of a PubMed record this reads. Titles and abstracts carry inline
// markup (<i>, <sup>), so they are taken as raw XML and flattened.
type pubmedSet struct {
	Articles []struct {
		Citation struct {
			PMID    string `xml:"PMID"`
			Article struct {
				Title   rawXML `xml:"ArticleTitle"`
				Journal struct {
					Title string `xml:"Title"`
					Issue struct {
						Date struct {
							Year        string `xml:"Year"`
							MedlineDate string `xml:"MedlineDate"`
						} `xml:"PubDate"`
					} `xml:"JournalIssue"`
				} `xml:"Journal"`
				Abstract struct {
					Texts []struct {
						Label string `xml:"Label,attr"`
						Body  string `xml:",innerxml"`
					} `xml:"AbstractText"`
				} `xml:"Abstract"`
				Authors []struct {
					Last       string `xml:"LastName"`
					Initials   string `xml:"Initials"`
					Collective string `xml:"CollectiveName"`
				} `xml:"AuthorList>Author"`
			} `xml:"Article"`
		} `xml:"MedlineCitation"`
		IDs []struct {
			Type  string `xml:"IdType,attr"`
			Value string `xml:",chardata"`
		} `xml:"PubmedData>ArticleIdList>ArticleId"`
	} `xml:"PubmedArticle"`
}

type rawXML struct {
	Inner string `xml:",innerxml"`
}

var (
	// blockTag is markup that separates words (a heading, a paragraph);
	// every other tag is inline (<i>, <sub>) and separates nothing, so
	// "H<sub>2</sub>O" must come out as "H2O" and not "H 2 O".
	blockTag    = regexp.MustCompile(`</?(?:p|h[1-6]|br|div|li|sec|title)\b[^>]*>`)
	tagPattern  = regexp.MustCompile(`<[^>]*>`)
	yearPattern = regexp.MustCompile(`\d{4}`)
)

// flatten turns a fragment of XML into its text on one line.
func flatten(fragment string) string {
	text := tagPattern.ReplaceAllString(blockTag.ReplaceAllString(fragment, " "), "")
	// What is left is XML character data; unescape it by decoding it.
	var decoded string
	if err := xml.Unmarshal([]byte("<x>"+text+"</x>"), &decoded); err == nil {
		text = decoded
	}
	return strings.Join(strings.Fields(text), " ")
}

func parsePubMed(raw []byte) (map[string]Paper, error) {
	var set pubmedSet
	if err := xml.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("PubMed's records are not readable")
	}
	out := make(map[string]Paper, len(set.Articles))
	for _, a := range set.Articles {
		article := a.Citation.Article
		paper := Paper{
			Title: flatten(article.Title.Inner),
			URL:   "https://pubmed.ncbi.nlm.nih.gov/" + a.Citation.PMID + "/",
			Venue: strings.TrimSpace(article.Journal.Title),
			Year:  article.Journal.Issue.Date.Year,
		}
		if paper.Year == "" {
			// "2019 Nov-Dec" and the like.
			paper.Year = yearPattern.FindString(article.Journal.Issue.Date.MedlineDate)
		}
		for _, author := range article.Authors {
			switch {
			case author.Last != "":
				paper.Authors = append(paper.Authors, strings.TrimSpace(author.Last+" "+author.Initials))
			case author.Collective != "":
				paper.Authors = append(paper.Authors, strings.TrimSpace(author.Collective))
			}
		}
		var sections []string
		for _, text := range article.Abstract.Texts {
			body := flatten(text.Body)
			if body == "" {
				continue
			}
			if text.Label != "" {
				body = text.Label + ": " + body
			}
			sections = append(sections, body)
		}
		paper.Abstract = strings.Join(sections, " ")
		for _, id := range a.IDs {
			if id.Type == "pmc" {
				paper.PMCID = strings.TrimSpace(id.Value)
			}
		}
		if a.Citation.PMID != "" && paper.Title != "" {
			out[a.Citation.PMID] = paper
		}
	}
	return out, nil
}

// pmcBody fetches a paper's body from PubMed Central and returns its text,
// cut to fullTextChars. An article whose body PMC does not release (it
// answers the front matter only) returns "".
func (s *Sources) pmcBody(ctx context.Context, pmcid string) (string, error) {
	raw, err := s.get(ctx, s.eutils+"/efetch.fcgi", s.ncbi(url.Values{
		"db": {"pmc"}, "id": {pmcid}, "retmode": {"xml"},
	}))
	if err != nil {
		return "", err
	}
	return jatsBody(raw, fullTextChars), nil
}

// jatsBody walks a JATS article and collects the text of its <body>:
// section titles and paragraphs, one per line. It is a token walk rather
// than a struct because a body nests sections to any depth and mixes text
// with inline elements. Tables, figures, formulas and reference lists are
// skipped: they are not prose and would spend the budget on cell contents.
func jatsBody(raw []byte, limit int) string {
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	// PMC's records declare a DTD and use named entities; do not fail on
	// one this decoder does not know.
	decoder.Strict = false
	var (
		out     strings.Builder
		block   strings.Builder
		inBody  bool
		depth   int // depth inside a block element (p or title)
		skipped int // depth inside an element whose text is not wanted
		size    int
	)
	skip := map[string]bool{"table-wrap": true, "fig": true, "disp-formula": true, "inline-formula": true, "ref-list": true, "supplementary-material": true, "xref": true}
	for size < limit {
		token, err := decoder.Token()
		if err == io.EOF || err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			name := t.Name.Local
			switch {
			case name == "body":
				inBody = true
			case !inBody:
			case skipped > 0 || skip[name]:
				skipped++
			case depth > 0:
				depth++
			case name == "p" || name == "title":
				depth = 1
				block.Reset()
			}
		case xml.EndElement:
			name := t.Name.Local
			switch {
			case name == "body":
				inBody = false
			case !inBody:
			case skipped > 0:
				skipped--
			case depth > 0:
				depth--
				if depth == 0 {
					if line := strings.Join(strings.Fields(block.String()), " "); line != "" {
						out.WriteString(line)
						out.WriteByte('\n')
						size += len([]rune(line)) + 1
					}
				}
			}
		case xml.CharData:
			if inBody && depth > 0 && skipped == 0 {
				block.Write(t)
			}
		}
	}
	text := strings.TrimSpace(out.String())
	if runes := []rune(text); len(runes) > limit {
		text = string(runes[:limit]) + "…"
	}
	return text
}

// OpenFullText is a paper's open-access body from PubMed Central, up to
// limit characters, or "" when PMC does not release it. It is what a PubMed
// search with full text reads, offered on its own so that importing a paper
// as a notebook source reads the same text through the same paced client.
func (s *Sources) OpenFullText(ctx context.Context, pmcid string, limit int) (string, error) {
	raw, err := s.get(ctx, s.eutils+"/efetch.fcgi", s.ncbi(url.Values{
		"db": {"pmc"}, "id": {pmcid}, "retmode": {"xml"},
	}))
	if err != nil {
		return "", err
	}
	return jatsBody(raw, limit), nil
}
