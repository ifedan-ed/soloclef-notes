// Package sourceidentity reads what a source's identity names: a PubMed id,
// a PubMed Central id, a DOI, an arXiv id or a web page's address, written
// the ways people and papers write them ("PMID: 38123456", "doi:10.1/x",
// "arXiv:2401.01234v2", a PubMed, doi.org or bioRxiv link).
//
// It is one package because two places must read an identity the same way
// and disagree about nothing: the chat's add_sources tool, which asks the
// person before a call that would read a web page (a request to an address
// the model chose), and the import behind it
// (soloclef_api/internal/notebookresearch), which looks each identity up at
// its source. If the two read one string differently, a page could be read
// without the question, or a paper refused for no reason.
//
// Only the identities that go to a fixed index in a fixed shape are papers:
// a PubMed or PubMed Central id, a DOI (resolved through doi.org), an arXiv
// id, and the canonical links of each. Every other http(s) address is a
// page.
package sourceidentity

import (
	"net/url"
	"regexp"
	"strings"
)

// Kind is what an identity names.
type Kind int

const (
	PMID Kind = iota + 1
	PMCID
	DOI
	Arxiv
	// Page is any other public web address.
	Page
)

// Identity is one identity, read.
type Identity struct {
	Kind Kind
	// ID is the normalised id: digits for a PMID, "PMC" and digits for a
	// PubMed Central id, the lower-case DOI, the arXiv id without its
	// version. Empty for a page.
	ID string
	// Address is a page's address; nil for a paper.
	Address *url.URL
	// Text is the identity as written, without surrounding space or angle
	// brackets.
	Text string
}

// NotIdentity is the reason for text that is none of the five, in the words
// the model passes on.
const NotIdentity = "That isn't a PubMed id, DOI, arXiv id, PubMed Central id or web address."

// MaxAddressChars is the longest address taken; browsers and servers agree
// on about this much.
const MaxAddressChars = 2048

var (
	// The bare forms. Each may carry the prefix people write before it.
	pmidRef  = regexp.MustCompile(`^(?i:pmid:?)?\s*(\d{1,9})$`)
	pmcidRef = regexp.MustCompile(`^(?i:pmcid:?)?\s*((?i:pmc)\d{1,9})$`)
	// arXiv's two id styles, new (2401.01234) and old (hep-th/9901001), the
	// latter's archive in lower case as arXiv writes it.
	arxivRef = regexp.MustCompile(`^(?i:arxiv:?)?\s*(\d{4}\.\d{4,5}|[a-z\-]+(?:\.[A-Z]{2})?/\d{7})(?:v\d+)?$`)
	doiRef   = regexp.MustCompile(`^(?i:doi:?)?\s*(10\.\d{4,9}/\S+)$`)
	// arXiv's own DOI for a paper names its arXiv id.
	arxivDOI = regexp.MustCompile(`^10\.48550/arxiv\.(.+)$`)
	// arxivShape is an arXiv id with no version.
	arxivShape = regexp.MustCompile(`^(\d{4}\.\d{4,5}|[a-z\-]+(\.[A-Z]{2})?/\d{7})$`)

	// The addresses the sources give their papers, read for the id inside.
	pubmedPath = regexp.MustCompile(`^/(\d{1,9})/?$`)
	pmcPath    = regexp.MustCompile(`^(?:/pmc)?/articles/((?i:pmc)\d{1,9})/?`)
	arxivPath  = regexp.MustCompile(`^/(?:abs|pdf)/(.+?)(?:v\d+)?(?:\.pdf)?/?$`)
	doiPath    = regexp.MustCompile(`^/(10\.\d{4,9}/.+)$`)
	// bioRxiv and medRxiv: /content/<doi>[v2][.full.pdf | .abstract].
	rxivPath = regexp.MustCompile(`^/content/(10\.\d{4,9}/[^/]+?)(?:v\d+)?(?:\.full.*|\.abstract)?/?$`)
)

// Read reads one identity, or says why it is none.
func Read(raw string) (Identity, string) {
	text := strings.Trim(strings.TrimSpace(raw), "<>")
	if address, _, ok := WebAddress(text); ok {
		return readAddress(address, text), ""
	}
	switch {
	case pmidRef.MatchString(text):
		return Identity{Kind: PMID, ID: pmidRef.FindStringSubmatch(text)[1], Text: text}, ""
	case pmcidRef.MatchString(text):
		return Identity{Kind: PMCID, ID: strings.ToUpper(pmcidRef.FindStringSubmatch(text)[1]), Text: text}, ""
	case arxivRef.MatchString(text):
		return Identity{Kind: Arxiv, ID: arxivRef.FindStringSubmatch(text)[1], Text: text}, ""
	case doiRef.MatchString(text):
		return doiIdentity(doiRef.FindStringSubmatch(text)[1], text), ""
	}
	return Identity{}, NotIdentity
}

// WebAddress is the trimmed address when it is an http(s) one with a host,
// which is the only kind that can be read, cited and opened.
func WebAddress(raw string) (*url.URL, string, bool) {
	text := strings.TrimSpace(raw)
	if text == "" || len(text) > MaxAddressChars {
		return nil, "", false
	}
	parsed, err := url.Parse(text)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, "", false
	}
	return parsed, text, true
}

// IsArxivID reports whether id is an arXiv id with no version.
func IsArxivID(id string) bool { return arxivShape.MatchString(id) }

// readAddress is a paper's id when the address is a paper's page at one of
// the sources, and the page itself otherwise.
func readAddress(address *url.URL, text string) Identity {
	host := strings.TrimPrefix(strings.ToLower(address.Hostname()), "www.")
	path := address.Path
	switch {
	case host == "pubmed.ncbi.nlm.nih.gov" && pubmedPath.MatchString(path):
		return Identity{Kind: PMID, ID: pubmedPath.FindStringSubmatch(path)[1], Text: text}
	case (host == "ncbi.nlm.nih.gov" || host == "pmc.ncbi.nlm.nih.gov") && pmcPath.MatchString(path):
		return Identity{Kind: PMCID, ID: strings.ToUpper(pmcPath.FindStringSubmatch(path)[1]), Text: text}
	case (host == "arxiv.org" || host == "export.arxiv.org") && arxivPath.MatchString(path):
		if id := arxivPath.FindStringSubmatch(path)[1]; arxivShape.MatchString(id) {
			return Identity{Kind: Arxiv, ID: id, Text: text}
		}
	case (host == "doi.org" || host == "dx.doi.org") && doiPath.MatchString(path):
		return doiIdentity(doiPath.FindStringSubmatch(path)[1], text)
	case (host == "biorxiv.org" || host == "medrxiv.org") && rxivPath.MatchString(path):
		return doiIdentity(rxivPath.FindStringSubmatch(path)[1], text)
	}
	return Identity{Kind: Page, Address: address, Text: text}
}

// doiIdentity is a DOI, or the arXiv id an arXiv DOI names.
func doiIdentity(doi, text string) Identity {
	doi = strings.ToLower(strings.TrimRight(doi, ".,;)"))
	if match := arxivDOI.FindStringSubmatch(doi); match != nil {
		// arXiv's DOIs are lower-cased like any other, and its new-style
		// ids have no letters; an old-style one keeps its archive's case.
		if id := match[1]; arxivShape.MatchString(id) {
			return Identity{Kind: Arxiv, ID: id, Text: text}
		}
	}
	return Identity{Kind: DOI, ID: doi, Text: text}
}
