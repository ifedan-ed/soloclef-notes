package sourceidentity

import "testing"

// Each identity is read as people and papers write it, and anything that is
// none of them says so.
func TestIdentitiesAreReadAsTheyAreWritten(t *testing.T) {
	for raw, want := range map[string]struct {
		kind Kind
		id   string
	}{
		"38123456":       {PMID, "38123456"},
		"PMID: 38123456": {PMID, "38123456"},
		"pmid38123456":   {PMID, "38123456"},
		"https://pubmed.ncbi.nlm.nih.gov/38123456/": {PMID, "38123456"},
		"PMC1234567":        {PMCID, "PMC1234567"},
		"pmcid: pmc1234567": {PMCID, "PMC1234567"},
		"https://pmc.ncbi.nlm.nih.gov/articles/PMC1234567/":                    {PMCID, "PMC1234567"},
		"https://www.ncbi.nlm.nih.gov/pmc/articles/PMC12345/":                  {PMCID, "PMC12345"},
		"10.1056/NEJMoa2034577":                                                {DOI, "10.1056/nejmoa2034577"},
		"doi:10.1056/NEJMoa2034577.":                                           {DOI, "10.1056/nejmoa2034577"},
		"https://doi.org/10.1056/NEJMoa2034577":                                {DOI, "10.1056/nejmoa2034577"},
		"https://www.biorxiv.org/content/10.1101/2024.01.15.575678v2.full.pdf": {DOI, "10.1101/2024.01.15.575678"},
		"2401.01234":                           {Arxiv, "2401.01234"},
		"arXiv:2401.01234v3":                   {Arxiv, "2401.01234"},
		"hep-th/9901001":                       {Arxiv, "hep-th/9901001"},
		"https://arxiv.org/abs/2401.01234v2":   {Arxiv, "2401.01234"},
		"https://arxiv.org/pdf/2401.01234.pdf": {Arxiv, "2401.01234"},
		"10.48550/arXiv.2401.01234":            {Arxiv, "2401.01234"},
		"https://example.org/tides":            {Page, ""},
		"<https://example.org/tides>":          {Page, ""},
		"https://arxiv.org/list/cs.LG":         {Page, ""},
	} {
		got, problem := Read(raw)
		if problem != "" || got.Kind != want.kind || got.ID != want.id {
			t.Errorf("%q: %+v %q, want %v %q", raw, got, problem, want.kind, want.id)
		}
	}
	for _, raw := range []string{"", "the tides paper", "ftp://example.org/x", "1234567890", "PMC", "doi:10.1/"} {
		if _, problem := Read(raw); problem != NotIdentity {
			t.Errorf("%q was read as an identity", raw)
		}
	}
}
