package notebookresearch

import (
	"net/url"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	address, _, ok := webAddress(raw)
	if !ok {
		t.Fatalf("%q is not a web address", raw)
	}
	return address
}

func TestTwoAddressesOfOnePageAreOneSource(t *testing.T) {
	same := []string{
		"https://www.Example.org/a/b/?utm_source=x&b=2&a=1&fbclid=zz",
		"http://example.org/a/b?a=1&b=2",
		"https://example.org:443/a/b?b=2&a=1&ref=home",
	}
	first := sameSourceKey(mustParse(t, same[0]))
	if first != "example.org/a/b?a=1&b=2" {
		t.Errorf("key = %q", first)
	}
	for _, address := range same[1:] {
		if key := sameSourceKey(mustParse(t, address)); key != first {
			t.Errorf("%s has key %q, want %q", address, key, first)
		}
	}
	for _, other := range []string{"https://example.org/a/b?a=2&b=2", "https://example.org:8443/a/b?a=1&b=2", "https://example.org/a/c?a=1&b=2"} {
		if sameSourceKey(mustParse(t, other)) == first {
			t.Errorf("%s was taken for the same page", other)
		}
	}
	if resultID(mustParse(t, same[0])) != resultID(mustParse(t, same[1])) || importIdentity(mustParse(t, same[0])) != importIdentity(mustParse(t, same[2])) {
		t.Error("one page has two ids")
	}
}

func TestOnlyAWebAddressIsTakenAndAPrivateOneIsTurnedDown(t *testing.T) {
	for _, bad := range []string{"", "   ", "ftp://example.org/x", "javascript:alert(1)", "https://", "example.org/page", "https://example.org/" + strings.Repeat("a", 2048)} {
		if _, _, ok := webAddress(bad); ok {
			t.Errorf("%q was taken as a web address", bad)
		}
	}
	for address, private := range map[string]bool{
		"http://localhost:8000/admin":    true,
		"http://api.localhost/":          true,
		"http://127.0.0.1/":              true,
		"http://10.1.2.3/":               true,
		"http://192.168.1.1/router":      true,
		"http://169.254.169.254/latest/": true,
		"http://[::1]/":                  true,
		"http://[::ffff:10.0.0.1]/":      true,
		"https://example.org/":           false,
		"https://93.184.216.34/":         false,
	} {
		if got := plainlyPrivate(mustParse(t, address)); got != private {
			t.Errorf("plainlyPrivate(%s) = %v", address, got)
		}
	}
}

func TestWhatAnImportMaySay(t *testing.T) {
	long := strings.Repeat("s", 4001)
	for name, c := range map[string]struct {
		body  string
		count int
		field string
	}{
		"two results":                {`{"items":[{"url":"https://a.example/1","title":"One"},{"url":"https://a.example/2","title":"Two","snippet":"s","source":"exa"}]}`, 2, ""},
		"the same page twice":        {`{"items":[{"url":"https://a.example/1","title":"One"},{"url":"http://www.a.example/1/?utm_medium=x","title":"Again"}]}`, 1, ""},
		"a paper":                    {`{"items":[{"url":"https://pubmed.ncbi.nlm.nih.gov/111/","title":"P","source":"pubmed","paper":{"pmid":"111","pmcid":"900","doi":"https://doi.org/10.1000/ABC","authors":["A"],"badges":["pubmed","new-badge"]}}]}`, 1, ""},
		"no items":                   {`{"items":[]}`, 0, "items"},
		"not a web address":          {`{"items":[{"url":"file:///etc/passwd","title":"x"}]}`, 0, "url"},
		"no title":                   {`{"items":[{"url":"https://a.example/1","title":"  "}]}`, 0, "title"},
		"a snippet too long":         {`{"items":[{"url":"https://a.example/1","title":"x","snippet":"` + long + `"}]}`, 0, "snippet"},
		"an unknown source":          {`{"items":[{"url":"https://a.example/1","title":"x","source":"reddit"}]}`, 0, "source"},
		"a paper id that is not one": {`{"items":[{"url":"https://a.example/1","title":"x","paper":{"pmid":"1 OR 1=1"}}]}`, 0, "paper"},
		"not an object":              {`"items"`, 0, "body"},
	} {
		items, location, problem := readItems([]byte(c.body))
		if c.field != "" {
			if problem == "" || location[len(location)-1] != c.field {
				t.Errorf("%s: refused at %v (%q), want %s", name, location, problem, c.field)
			}
			continue
		}
		if problem != "" || len(items) != c.count {
			t.Errorf("%s: %d items, problem %q", name, len(items), problem)
		}
		if name == "a paper" {
			paper := items[0].paper
			// Ids are put in one spelling; a badge the apps do not draw
			// is not stored.
			if paper.PMCID != "PMC900" || paper.DOI != "10.1000/abc" || len(paper.Badges) != 1 {
				t.Errorf("paper = %+v", paper)
			}
		}
	}
	// More than fifty is refused whole, not cut to fifty.
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, `{"url":"https://a.example/`+strings.Repeat("x", i+1)+`","title":"t"}`)
	}
	if _, location, problem := readItems([]byte(`{"items":[` + strings.Join(many, ",") + `]}`)); problem == "" || location[1] != "items" {
		t.Errorf("51 items: %v %q", location, problem)
	}
}

func TestAPapersIdsAreReadFromItsAddress(t *testing.T) {
	for name, c := range map[string]struct {
		paper  academic.Paper
		source string
		check  func(Paper) bool
	}{
		"PubMed":  {academic.Paper{URL: "https://pubmed.ncbi.nlm.nih.gov/12345/", Year: "2020"}, "pubmed", func(p Paper) bool { return p.PMID == "12345" && *p.Year == 2020 && !p.OpenAccess }},
		"arXiv":   {academic.Paper{URL: "https://arxiv.org/abs/2401.01234"}, "arxiv", func(p Paper) bool { return p.ArxivID == "2401.01234" && p.OpenAccess && p.Year == nil }},
		"medRxiv": {academic.Paper{URL: "https://doi.org/10.1101/2024.01.01.123", Venue: "medRxiv (preprint)"}, "medrxiv", func(p Paper) bool { return p.DOI == "10.1101/2024.01.01.123" && p.Server == "medrxiv" }},
		// An id of the wrong shape is left out; the row stays a result.
		"an odd arXiv address": {academic.Paper{URL: "https://arxiv.org/abs/not an id"}, "arxiv", func(p Paper) bool { return p.ArxivID == "" && len(p.Badges) == 1 }},
	} {
		if got := paperOf(c.paper, c.source); !c.check(*got) {
			t.Errorf("%s: paper = %+v", name, *got)
		}
	}
}

func TestAnExcerptIsWhereTheQueryIs(t *testing.T) {
	text := strings.Repeat("alpha ", 100) + "the neap tide is weakest " + strings.Repeat("omega ", 100)
	got := excerpt(text, "of neap", 80)
	if !strings.Contains(got, "neap tide") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || len([]rune(got)) > 82 {
		t.Errorf("excerpt = %q", got)
	}
	// No word of the query in the text: the opening, and no leading mark.
	if got := excerpt(text, "zebra", 30); !strings.HasPrefix(got, "alpha alpha") {
		t.Errorf("excerpt = %q", got)
	}
	if got := excerpt("short text", "text", 80); got != "short text" {
		t.Errorf("excerpt = %q", got)
	}
	if got := trim("  one\n two   three ", 9); got != "one two…" {
		t.Errorf("trim = %q", got)
	}
}

// An arXiv paper is read from its PDF, which is the paper; anything else
// at the address it was listed under.
func TestAnArxivPaperIsReadFromItsPDF(t *testing.T) {
	for want, about := range map[string]imported{
		"https://arxiv.org/pdf/2401.00001":     {URL: "https://arxiv.org/abs/2401.00001", Paper: &Paper{ArxivID: "2401.00001"}},
		"https://arxiv.org/pdf/hep-th/9901001": {URL: "https://arxiv.org/abs/hep-th/9901001", Paper: &Paper{ArxivID: "hep-th/9901001"}},
		"https://pubmed.ncbi.nlm.nih.gov/111/": {URL: "https://pubmed.ncbi.nlm.nih.gov/111/", Paper: &Paper{PMID: "111"}},
		"https://example.org/report.pdf":       {URL: "https://example.org/report.pdf"},
		"":                                     {},
	} {
		if got := addressToRead(about); got != want {
			t.Errorf("%+v is read at %q, want %q", about, got, want)
		}
	}
}
