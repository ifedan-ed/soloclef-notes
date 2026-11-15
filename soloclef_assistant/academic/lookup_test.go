package academic

import (
	"context"
	"testing"
)

// Papers named by their ids are found in one search and one fetch, whatever
// mix of PMIDs, DOIs and PubMed Central ids names them, and each comes back
// carrying the ids it can be matched by.
func TestPubMedByIDAsksOnceForEveryKindOfID(t *testing.T) {
	f, s := newFake(t)
	papers, err := s.PubMedByID(context.Background(), []string{"222"}, []string{`10.1000/X"`}, []string{"PMC777"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 {
		t.Fatalf("%d requests, want a search and a fetch", len(f.requests))
	}
	if got := f.query(0)["term"][0]; got != `222[uid] OR "10.1000/X"[doi] OR PMC777[pmcid]` {
		t.Errorf("term = %q", got)
	}
	if len(papers) != 2 {
		t.Fatalf("papers = %+v", papers)
	}
	byPMID := map[string]Paper{}
	for _, paper := range papers {
		byPMID[paper.PMID] = paper
	}
	if first := byPMID["111"]; first.DOI != "10.1000/x" || first.PMCID != "PMC777" {
		t.Errorf("111 = %+v", first)
	}
	if second := byPMID["222"]; second.DOI != "" || second.Title != "Second paper." {
		t.Errorf("222 = %+v", second)
	}
}

func TestPubMedByIDAsksNothingForNoIDsAndRefusesTooMany(t *testing.T) {
	f, s := newFake(t)
	if papers, err := s.PubMedByID(context.Background(), nil, nil, nil); err != nil || papers != nil {
		t.Fatalf("papers=%v err=%v", papers, err)
	}
	many := make([]string, maxLookupIDs+1)
	for i := range many {
		many[i] = "1"
	}
	if _, err := s.PubMedByID(context.Background(), many, nil, nil); err == nil {
		t.Error("too many ids were sent")
	}
	if len(f.requests) != 0 {
		t.Errorf("%d requests", len(f.requests))
	}
}

func TestArxivByIDNamesTheIDsAndReadsTheEntries(t *testing.T) {
	f, s := newFake(t)
	papers, err := s.ArxivByID(context.Background(), []string{"2401.00001", "hep-th/9901001"})
	if err != nil {
		t.Fatal(err)
	}
	if q := f.query(0); q["id_list"][0] != "2401.00001,hep-th/9901001" || q["max_results"][0] != "2" {
		t.Errorf("asked %v", q)
	}
	if len(papers) != 1 || papers[0].URL != "https://arxiv.org/abs/2401.00001v2" {
		t.Errorf("papers = %+v", papers)
	}
}

// A DOI is looked for among the preprints by its own field, on the two
// servers only, and each preprint found carries its DOI in lower case.
func TestPreprintsByDOIAsksForTheDOIsOnTheTwoServers(t *testing.T) {
	f, s := newFake(t)
	papers, err := s.PreprintsByDOI(context.Background(), []string{"10.64898/2026.01.02.000001", "10.1/b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.query(0)["query"][0]; got != `(DOI:"10.64898/2026.01.02.000001" OR DOI:"10.1/b") AND SRC:PPR AND (PUBLISHER:"medRxiv" OR PUBLISHER:"bioRxiv")` {
		t.Errorf("query = %q", got)
	}
	if len(papers) != 2 || papers[0].DOI != "10.64898/2026.01.02.000001" {
		t.Errorf("papers = %+v", papers)
	}
}
