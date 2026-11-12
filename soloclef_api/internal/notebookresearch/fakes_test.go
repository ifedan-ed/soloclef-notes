package notebookresearch

import (
	"context"
	"errors"
	"strings"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// papers stands in for the scholarly sources. Nothing in these tests
// reaches the internet.
type papers struct {
	pubmed, arxiv, preprints []academic.Paper
	// failing is the error each source named here answers with.
	failing map[string]error
	// hold, when set, is waited on before PubMed answers.
	hold chan struct{}
	// fullText is PubMed Central's body by PMC id.
	fullText map[string]string

	mu    sync.Mutex
	asked []string
}

func (p *papers) note(call string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, call)
}

func (p *papers) PubMed(ctx context.Context, _ string, limit int, fullText bool, _, _ int) ([]academic.Paper, error) {
	p.note("pubmed")
	if p.hold != nil {
		select {
		case <-p.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := p.failing["pubmed"]; err != nil {
		return nil, err
	}
	out := append([]academic.Paper(nil), p.pubmed...)
	if fullText {
		for i := range out {
			out[i].FullText = p.fullText[out[i].PMCID]
		}
	}
	return out[:min(limit, len(out))], nil
}

func (p *papers) Arxiv(context.Context, string, int) ([]academic.Paper, error) {
	p.note("arxiv")
	return p.arxiv, p.failing["arxiv"]
}

func (p *papers) Preprints(_ context.Context, _, server string, _ int) ([]academic.Paper, error) {
	p.note("preprints:" + server)
	return p.preprints, p.failing["medrxiv"]
}

func (p *papers) OpenFullText(_ context.Context, pmcid string, _ int) (string, error) {
	p.note("fulltext:" + pmcid)
	return p.fullText[pmcid], nil
}

// web stands in for the account's keyed web search: results per provider,
// for the accounts that have connected it.
type web struct {
	connected map[string]map[websearch.Provider]bool
	results   map[websearch.Provider][]websearch.Result
	failing   map[websearch.Provider]error
}

func (w web) Connected(_ context.Context, userID string, provider websearch.Provider) (bool, error) {
	return w.connected[userID][provider], nil
}

func (w web) Find(_ context.Context, userID string, provider websearch.Provider, _ string, _ int) ([]websearch.Result, error) {
	if !w.connected[userID][provider] {
		return nil, websearch.ErrNotConnected
	}
	if err := w.failing[provider]; err != nil {
		return nil, err
	}
	return w.results[provider], nil
}

// pages stands in for the page reader: a page per address, a PDF's bytes
// per address, and a failure for any address it was not given.
type pages struct {
	byAddress map[string]pagereader.Page
	// pdfs are the addresses that are PDFs, with their bytes.
	pdfs map[string]string
	mu   sync.Mutex
	read []string
}

func (p *pages) Keep(ctx context.Context, address string, _ int64, keep pagereader.Keeper) pagereader.Page {
	p.mu.Lock()
	body, isPDF := p.pdfs[address]
	p.mu.Unlock()
	if !isPDF {
		return p.ReadWebPages(ctx, []string{address})[0]
	}
	p.mu.Lock()
	p.read = append(p.read, address)
	p.mu.Unlock()
	page := pagereader.Page{Asked: address, URL: address, Status: pagereader.Read, PDF: true}
	if err := keep(ctx, pagereader.PDF{URL: address, Name: "paper.pdf", Body: strings.NewReader(body)}); err != nil {
		page.Status, page.Problem = pagereader.Failed, "The PDF was downloaded but could not be stored."
	}
	return page
}

func (p *pages) ReadWebPages(_ context.Context, addresses []string) []pagereader.Page {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]pagereader.Page, len(addresses))
	for i, address := range addresses {
		p.read = append(p.read, address)
		page, known := p.byAddress[address]
		if !known {
			page = pagereader.Page{Status: pagereader.Failed, Problem: "The page could not be reached."}
		}
		page.Asked = address
		if page.URL == "" {
			page.URL = address
		}
		out[i] = page
	}
	return out
}

// indexing records the documents whose indexing was queued.
type indexing struct {
	mu     sync.Mutex
	queued []int64
	users  []string
}

func (i *indexing) Enqueue(_ context.Context, documentID int64, logContext produce.LogContext) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.queued = append(i.queued, documentID)
	i.users = append(i.users, logContext.UserID)
	return nil
}

var errUnreachable = errors.New("eutils.ncbi.nlm.nih.gov answered 503")
