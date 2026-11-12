package notebookresearch

import (
	"regexp"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
)

// Paper is what a result knows about itself as a paper: the ids it can be
// found again by, its byline, and whether its text is open. A search sends
// it with the row; the import sends it back, which is how a paper found on
// PubMed and again on a preprint server becomes one document, and how the
// read knows to fetch PubMed Central's full text.
type Paper struct {
	DOI     string `json:"doi,omitempty"`
	PMID    string `json:"pmid,omitempty"`
	PMCID   string `json:"pmcid,omitempty"`
	ArxivID string `json:"arxiv_id,omitempty"`
	// Server is "medrxiv" or "biorxiv" for a preprint from one of them.
	Server     string   `json:"server,omitempty"`
	Authors    []string `json:"authors"`
	Venue      string   `json:"venue,omitempty"`
	Year       *int     `json:"year,omitempty"`
	Categories []string `json:"categories"`
	Badges     []string `json:"badges"`
	OpenAccess bool     `json:"open_access"`
}

// The shapes of the four ids. They come back from a client on import and go
// into a query, so each is held to its shape and its length.
var (
	doiShape   = regexp.MustCompile(`^10\.\d{4,9}/\S{1,190}$`)
	pmidShape  = regexp.MustCompile(`^\d{1,9}$`)
	pmcidShape = regexp.MustCompile(`^PMC\d{1,9}$`)
	arxivShape = regexp.MustCompile(`^(\d{4}\.\d{4,5}|[a-z\-]+(\.[A-Z]{2})?/\d{7})$`)
	doiPrefix  = regexp.MustCompile(`^(https?://(dx\.)?doi\.org/|doi:)`)
	arxivVer   = regexp.MustCompile(`v\d+$`)
)

const (
	maxAuthors       = 30
	maxNameChars     = 120
	maxVenueChars    = 300
	maxCategories    = 12
	maxBadges        = 6
	minYear, maxYear = 1000, 3000
)

// knownBadges are the marks the apps draw; one they do not know is dropped
// here so it is never stored.
var knownBadges = map[string]bool{"pubmed": true, "pmc": true, "arxiv": true, "medrxiv": true, "biorxiv": true}

// checked returns the paper as it will be stored, or the name of the id
// that is not one. Ids are refused when wrong, because a wrong id would
// link the import to some other paper; everything else is descriptive and
// is trimmed to its bounds instead.
func (p Paper) checked() (Paper, string) {
	out := Paper{Authors: []string{}, Categories: []string{}, Badges: []string{}, OpenAccess: p.OpenAccess}
	if doi := doiPrefix.ReplaceAllString(strings.ToLower(strings.TrimSpace(p.DOI)), ""); doi != "" {
		if !doiShape.MatchString(doi) {
			return Paper{}, "doi"
		}
		out.DOI = doi
	}
	if pmid := strings.TrimSpace(p.PMID); pmid != "" {
		if !pmidShape.MatchString(pmid) {
			return Paper{}, "pmid"
		}
		out.PMID = pmid
	}
	if pmcid := strings.ToUpper(strings.TrimSpace(p.PMCID)); pmcid != "" {
		if pmidShape.MatchString(pmcid) {
			pmcid = "PMC" + pmcid
		}
		if !pmcidShape.MatchString(pmcid) {
			return Paper{}, "pmcid"
		}
		out.PMCID = pmcid
	}
	if id := arxivVer.ReplaceAllString(strings.TrimSpace(p.ArxivID), ""); id != "" {
		if !arxivShape.MatchString(id) {
			return Paper{}, "arxiv_id"
		}
		out.ArxivID = id
	}
	if p.Server == "medrxiv" || p.Server == "biorxiv" {
		out.Server = p.Server
	}
	out.Authors = shortStrings(p.Authors, maxAuthors)
	out.Categories = shortStrings(p.Categories, maxCategories)
	for _, badge := range p.Badges {
		if knownBadges[badge] && len(out.Badges) < maxBadges && !contains(out.Badges, badge) {
			out.Badges = append(out.Badges, badge)
		}
	}
	out.Venue = clip(strings.TrimSpace(p.Venue), maxVenueChars)
	if p.Year != nil && *p.Year >= minYear && *p.Year <= maxYear {
		year := *p.Year
		out.Year = &year
	}
	return out, ""
}

func shortStrings(values []string, most int) []string {
	out := make([]string, 0, min(len(values), most))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && len(out) < most {
			out = append(out, clip(value, maxNameChars))
		}
	}
	return out
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// clip cuts s to at most n characters, between characters.
func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// The addresses the scholarly sources give their papers, which is where a
// paper's ids are read from: the sources answer a title, a byline and an
// address, and the address is the id.
var (
	pubmedAddress = regexp.MustCompile(`^https://pubmed\.ncbi\.nlm\.nih\.gov/(\d{1,9})/?$`)
	arxivAddress  = regexp.MustCompile(`^https://arxiv\.org/abs/(.+)$`)
	doiAddress    = regexp.MustCompile(`^https://doi\.org/(10\..+)$`)
)

// paperOf describes a search's paper for the row that carries it.
func paperOf(found academic.Paper, source string) *Paper {
	paper := Paper{Authors: shortStrings(found.Authors, maxAuthors), Venue: clip(found.Venue, maxVenueChars)}
	if year, err := strconv.Atoi(found.Year); err == nil {
		paper.Year = &year
	}
	if match := pubmedAddress.FindStringSubmatch(found.URL); match != nil {
		paper.PMID = match[1]
	}
	if match := arxivAddress.FindStringSubmatch(found.URL); match != nil {
		paper.ArxivID = match[1]
	}
	if match := doiAddress.FindStringSubmatch(found.URL); match != nil {
		paper.DOI = match[1]
	}
	paper.PMCID = found.PMCID
	switch source {
	case "pubmed":
		paper.Badges = append(paper.Badges, "pubmed")
		if found.PMCID != "" {
			// In PubMed Central: the body can be read without a subscription.
			paper.Badges = append(paper.Badges, "pmc")
			paper.OpenAccess = true
		}
	case "arxiv":
		paper.Badges = append(paper.Badges, "arxiv")
		paper.OpenAccess = true
	case "medrxiv":
		// The preprint search covers both servers; the venue says which
		// one a paper is on ("bioRxiv (preprint)").
		paper.Server = "medrxiv"
		if strings.HasPrefix(strings.ToLower(found.Venue), "biorxiv") {
			paper.Server = "biorxiv"
		}
		paper.Badges = append(paper.Badges, paper.Server)
		paper.OpenAccess = true
	}
	// A source's own id that does not have an id's shape is left out and the
	// row is still a result: it can be opened and imported by its address.
	for {
		checked, bad := paper.checked()
		switch bad {
		case "":
			return &checked
		case "doi":
			paper.DOI = ""
		case "pmid":
			paper.PMID = ""
		case "pmcid":
			paper.PMCID = ""
		case "arxiv_id":
			paper.ArxivID = ""
		}
	}
}
