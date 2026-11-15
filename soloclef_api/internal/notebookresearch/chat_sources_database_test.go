package notebookresearch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/sourceidentity"
)

// The chat's add_sources, over the import the research box uses, a real
// database and stand-ins for the sources and the page reader.

func chatSources(h *harness) ChatSources {
	adder := ChatSources{Imports: Imports{Pool: h.pool, Reads: h.reads}, Lookup: h.papers}
	if err := adder.Check(); err != nil {
		h.t.Fatal(err)
	}
	return adder
}

// approved is a call the person approved, outside any conversation: what
// an add of pages needs. The chat's own rules for pages are
// chat_pages_database_test.go's.
func approved(userID string, notebookID int64, refs ...string) addsources.Request {
	return addsources.Request{UserID: userID, NotebookID: notebookID, Refs: refs, PagesApproved: true}
}

func statuses(outcomes []addsources.Outcome) string {
	var words []string
	for _, outcome := range outcomes {
		words = append(words, outcome.Status)
	}
	return strings.Join(words, " ")
}

func TestTheChatAddsPapersAndPagesThroughTheImport(t *testing.T) {
	h := newHarness(t)
	const alice = testpostgres.Alice
	h.papers.pubmed = []academic.Paper{tidesPaper, seaPaper}
	h.papers.arxiv = []academic.Paper{arxivPaper}
	adder := chatSources(h)
	ctx := context.Background()

	out, err := adder.Add(ctx, approved(alice, 10,
		"PMID: 111", "https://arxiv.org/abs/2401.01234", "99999999", "the tides paper",
		"https://tides.example/tables", "http://127.0.0.1/admin",
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(out); got != "added added not_added not_added added not_added" {
		t.Fatalf("outcomes %s: %+v", got, out)
	}
	if out[0].Title != "Tides and the moon" || !out[0].Reading || out[0].DocumentID == 0 || out[0].URL != "https://pubmed.ncbi.nlm.nih.gov/111/" {
		t.Errorf("the PubMed paper: %+v", out[0])
	}
	if out[2].Reason != noSuchPMID || out[3].Reason != sourceidentity.NotIdentity || out[5].Reason != notPublic {
		t.Errorf("reasons: %q %q %q", out[2].Reason, out[3].Reason, out[5].Reason)
	}
	// One search and one fetch for every PubMed id, not one each.
	if got := strings.Count(strings.Join(h.papers.asked, " "), "pubmed-by-id"); got != 1 {
		t.Errorf("PubMed was asked %d times", got)
	}

	h.settle()
	// Read as the research box reads them: the paper's open full text from
	// PubMed Central, the page's text, each queued for indexing.
	if h.count(`documents WHERE user_id = '`+alice+`' AND document_metadata ->> 'read_from' = 'pubmed_central'
		AND document_metadata -> 'paper' ->> 'pmid' = '111' AND content LIKE '%We watched the sea.%'`) != 1 {
		t.Error("the PubMed paper was not read from PubMed Central")
	}
	if h.count(`documents WHERE user_id = '`+alice+`' AND document_metadata ->> 'read_from' = 'page' AND title = 'Tide tables'`) != 1 {
		t.Error("the page was not read")
	}
	if h.count(`documents WHERE user_id = '`+alice+`' AND document_metadata -> 'paper' ->> 'arxiv_id' = '2401.01234'`) != 1 {
		t.Error("the arXiv paper is not a document with its id")
	}
	if h.count("notebook_sources WHERE notebook_id = 10 AND enabled") != 3 || len(h.indexing.queued) != 3 {
		t.Errorf("%d sources, %d indexed", h.count("notebook_sources WHERE notebook_id = 10"), len(h.indexing.queued))
	}

	// Read, and waiting for its indexing job: still "reading", because the
	// search cannot find it yet. Once indexed it is not.
	if h.count("documents WHERE status ->> 'state' = 'pending'") != 3 {
		t.Error("the read documents are not waiting for their indexing")
	}
	testpostgres.Exec(t, h.pool, `UPDATE documents SET status = '{"state": "ready"}'`)

	// The same paper again, by its PMID and by its PubMed address, in one
	// call: one source, and both told it was one already.
	again, err := adder.Add(ctx, approved(alice, 10, "111", "https://pubmed.ncbi.nlm.nih.gov/111/"))
	if err != nil {
		t.Fatal(err)
	}
	if statuses(again) != "already_a_source already_a_source" || again[0].DocumentID != out[0].DocumentID || again[1].DocumentID != out[0].DocumentID || again[0].Reading {
		t.Errorf("again: %+v", again)
	}
	if h.count("documents WHERE user_id = '"+alice+"'") != 3 {
		t.Error("adding a paper twice made a second document")
	}
	// The research box finds the chat's paper by its id: one import.
	status, body := h.as(alice, "POST", "/api/v1/notebooks/10/research/import",
		`{"items":[{"url":"https://pubmed.ncbi.nlm.nih.gov/111/","title":"Tides and the moon","source":"pubmed","paper":{"pmid":"111"}}]}`)
	if status != 200 || h.count("documents WHERE user_id = '"+alice+"'") != 3 {
		t.Errorf("the box's import of the chat's paper: %d %s", status, body)
	}
}

// add_sources adds only to the notebook the chat belongs to, owned by the
// caller: her notebook asked by him, and his asked by her, write nothing.
func TestTheChatCannotAddToAnotherAccountsNotebook(t *testing.T) {
	h := newHarness(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	adder := chatSources(h)
	ctx := context.Background()
	for _, attempt := range []struct {
		user     string
		notebook int64
	}{{bob, 10}, {alice, 20}, {alice, 999}} {
		if _, err := adder.Add(ctx, approved(attempt.user, attempt.notebook, "111", "https://tides.example/tables")); !errors.Is(err, addsources.ErrNotebookAbsent) {
			t.Errorf("%s into %d: %v", attempt.user, attempt.notebook, err)
		}
	}
	h.settle()
	if h.count("documents") != 0 || h.count("notebook_sources") != 0 || len(h.indexing.queued) != 0 {
		t.Error("a refused add left rows or queued work")
	}
	// His own notebook takes the paper as a document of his own.
	out, err := adder.Add(ctx, approved(bob, 20, "111"))
	if err != nil || statuses(out) != "added" {
		t.Fatalf("his own: %+v %v", out, err)
	}
	h.settle()
	if h.count("documents WHERE user_id = '"+bob+"'") != 1 || h.count("documents WHERE user_id = '"+alice+"'") != 0 {
		t.Error("his paper is not his alone")
	}
}

// A source that cannot be reached is said, by name, and costs nothing else
// in the call: a DOI is found on PubMed, as a preprint, or failing both at
// its publisher's page.
func TestTheChatSaysWhichSourceCouldNotBeReached(t *testing.T) {
	h := newHarness(t)
	const alice = testpostgres.Alice
	h.papers.preprints = []academic.Paper{bioPaper}
	adder := chatSources(h)
	ctx := context.Background()

	out, err := adder.Add(ctx, approved(alice, 10, "10.1101/2024.01.01.123456", "10.5555/elsewhere"))
	if err != nil {
		t.Fatal(err)
	}
	if statuses(out) != "added added" || out[0].Title != "Tidal biology" || out[1].URL != "https://doi.org/10.5555/elsewhere" {
		t.Errorf("the DOIs: %+v", out)
	}
	h.papers.failing = map[string]error{"pubmed": errUnreachable, "arxiv": errUnreachable}
	out, err = adder.Add(ctx, approved(alice, 10, "222", "2401.01234", "https://tides.example/tables"))
	if err != nil {
		t.Fatal(err)
	}
	if statuses(out) != "not_added not_added added" || out[0].Reason != pubmedUnreachable || out[1].Reason != arxivUnreachable {
		t.Errorf("with two sources down: %+v", out)
	}
	h.settle()
}

// A PubMed paper whose body is not open is read from PubMed's record, its
// whole abstract, and not from its PubMed page (drawn by script, so the
// reader found nothing there and the source said it was not read).
func TestAPubMedPaperWithoutAnOpenBodyIsItsAbstract(t *testing.T) {
	h := newHarness(t)
	const alice = testpostgres.Alice
	h.papers.pubmed = []academic.Paper{seaPaper}
	out, err := chatSources(h).Add(context.Background(), approved(alice, 10, "222"))
	if err != nil || statuses(out) != "added" {
		t.Fatalf("%+v %v", out, err)
	}
	h.settle()
	if h.count(`documents WHERE document_metadata ->> 'read_from' = 'pubmed_abstract' AND document_metadata ->> 'fetched' = 'true'
		AND content LIKE '%## Abstract%Levels rise.%'`) != 1 {
		t.Error("the paper was not read from its abstract")
	}
	for _, address := range h.pages.read {
		if strings.Contains(address, "pubmed.ncbi.nlm.nih.gov") {
			t.Errorf("its PubMed page was read: %s", address)
		}
	}
}
