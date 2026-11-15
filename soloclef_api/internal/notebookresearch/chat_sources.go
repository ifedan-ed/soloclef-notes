package notebookresearch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/sourceidentity"
)

// PaperLookup finds papers by their ids (*academic.Sources): the chat names
// a paper by its id, and the import needs the source's record of it.
type PaperLookup interface {
	PubMedByID(ctx context.Context, pmids, dois, pmcids []string) ([]academic.Paper, error)
	ArxivByID(ctx context.Context, ids []string) ([]academic.Paper, error)
	PreprintsByDOI(ctx context.Context, dois []string) ([]academic.Paper, error)
}

// ChatSources is the chat's add_sources tool's way in (addsources.Adder):
// each identity looked up at its source, made into the item the research
// box would have imported for the same paper, and imported through the same
// Imports. So the chat and the box find a paper again by the same ids, and
// read the same PubMed Central full text, the same arXiv PDF and the same
// pages.
type ChatSources struct {
	Imports Imports
	Lookup  PaperLookup
}

// Check reports what is missing. Called once at start-up.
func (c ChatSources) Check() error {
	switch {
	case c.Imports.Pool == nil || c.Imports.Reads == nil:
		return errors.New("notebookresearch: the chat's sources need the pool and the reads")
	case c.Lookup == nil:
		return errors.New("notebookresearch: the chat's sources need the paper lookup")
	}
	return nil
}

// The reasons a source is not added, in the words the model passes on.
const (
	pubmedUnreachable   = "PubMed could not be reached just now. Try again in a minute."
	arxivUnreachable    = "arXiv could not be reached just now. Try again in a minute."
	preprintUnreachable = "The preprint index could not be reached just now, so it is not known whether this DOI is a preprint. Try again in a minute."
	noSuchPMID          = "PubMed has no paper with this id."
	noSuchPMCID         = "PubMed has no paper with this PubMed Central id."
	noSuchArxiv         = "arXiv has no paper with this id."
)

// Add adds the identities to the notebook, answering one outcome each, in
// order. The notebook is proved the account's inside the import's
// transaction; a notebook that is not is addsources.ErrNotebookAbsent, with
// nothing written. A web page is read only as chat_pages.go allows.
func (c ChatSources) Add(ctx context.Context, request addsources.Request) ([]addsources.Outcome, error) {
	refs := request.Refs
	outcomes := make([]addsources.Outcome, len(refs))
	references := make([]sourceidentity.Identity, len(refs))
	refused := make([]string, len(refs))
	for i, ref := range refs {
		outcomes[i].Ref = ref
		read, problem := sourceidentity.Read(ref)
		if problem != "" {
			outcomes[i].Status, outcomes[i].Reason = addsources.StatusNotAdded, problem
			continue
		}
		references[i] = read
	}
	if err := c.pageAddresses(ctx, request, references, refused); err != nil {
		return nil, fmt.Errorf("reading which addresses the user wrote: %w", err)
	}
	for i, problem := range refused {
		if problem != "" {
			// Not looked up at all: an unapproved page is not read, and
			// resolve skips an identity with no kind.
			outcomes[i].Status, outcomes[i].Reason = addsources.StatusNotAdded, problem
			references[i] = sourceidentity.Identity{}
		}
	}
	found, problems := c.resolve(ctx, references)
	// One import of every item found, each page once: two identities of one
	// paper (its PMID and its DOI) are one source, and both are told so.
	var items []item
	itemOf := make([]int, len(refs))
	seen := map[string]int{}
	for i := range refs {
		itemOf[i] = -1
		if problem := problems[i]; problem != "" {
			outcomes[i].Status, outcomes[i].Reason = addsources.StatusNotAdded, problem
			continue
		}
		if found[i] == nil {
			if outcomes[i].Status == "" {
				// A record whose address cannot be read: the source
				// answered, with nothing to import.
				outcomes[i].Status, outcomes[i].Reason = addsources.StatusNotAdded, "The source's record of it has no address to read."
			}
			continue
		}
		key := found[i].key()
		if at, known := seen[key]; known {
			itemOf[i] = at
			continue
		}
		seen[key] = len(items)
		itemOf[i] = len(items)
		items = append(items, *found[i])
	}
	if len(items) == 0 {
		return outcomes, nil
	}
	imported, err := c.Imports.Add(ctx, Importing{UserID: request.UserID, NotebookID: request.NotebookID, RequestID: requestcontext.ID(ctx)}, items)
	if errors.Is(err, errNotebookAbsent) {
		return nil, addsources.ErrNotebookAbsent
	}
	if err != nil {
		return nil, err
	}
	for i := range refs {
		at := itemOf[i]
		if at < 0 {
			continue
		}
		one := imported[at]
		outcomes[i].Status = addsources.StatusAdded
		if one.WasSource {
			outcomes[i].Status = addsources.StatusAlready
		}
		outcomes[i].DocumentID, outcomes[i].Title, outcomes[i].URL, outcomes[i].Reading = one.DocumentID, one.Title, items[at].url, one.Reading
	}
	return outcomes, nil
}

// key is what two items of one source share: the same page, or the same
// paper by one of its ids.
func (it item) key() string {
	if it.paper != nil {
		switch {
		case it.paper.PMID != "":
			return "pmid:" + it.paper.PMID
		case it.paper.ArxivID != "":
			return "arxiv:" + it.paper.ArxivID
		case it.paper.DOI != "":
			return "doi:" + it.paper.DOI
		}
	}
	return "page:" + sameSourceKey(it.address)
}

// resolve looks every reference up at its source, in one request per source
// for all of them (two for PubMed: a search and a fetch). It answers, per
// reference, the item to import or why there is none. A reference that was
// not recognised has neither.
func (c ChatSources) resolve(ctx context.Context, references []sourceidentity.Identity) ([]*item, []string) {
	found := make([]*item, len(references))
	problems := make([]string, len(references))
	var pmids, dois, pmcids, arxivIDs []string
	for i, ref := range references {
		switch ref.Kind {
		case sourceidentity.PMID:
			pmids = append(pmids, ref.ID)
		case sourceidentity.PMCID:
			pmcids = append(pmcids, ref.ID)
		case sourceidentity.DOI:
			dois = append(dois, ref.ID)
		case sourceidentity.Arxiv:
			arxivIDs = append(arxivIDs, ref.ID)
		case sourceidentity.Page:
			if plainlyPrivate(ref.Address) {
				problems[i] = notPublic
				continue
			}
			// Titled with its own address until the page says what it is
			// called, as a pasted link is (readPage).
			found[i] = &item{address: ref.Address, url: ref.Address.String(), title: clip(ref.Address.String(), maxTitleChars), source: "web"}
		}
	}

	var pubmed []academic.Paper
	var pubmedErr error
	if len(pmids)+len(dois)+len(pmcids) > 0 {
		pubmed, pubmedErr = c.Lookup.PubMedByID(ctx, pmids, dois, pmcids)
	}
	// A DOI PubMed does not know may be a preprint, which the preprint
	// index has with its abstract; one that is neither is read from its
	// publisher's page, where doi.org sends it.
	var notInPubMed []string
	for _, ref := range references {
		if ref.Kind == sourceidentity.DOI && pubmedErr == nil && paperBy(pubmed, func(p academic.Paper) bool { return p.DOI == ref.ID }) == nil {
			notInPubMed = append(notInPubMed, ref.ID)
		}
	}
	var preprints []academic.Paper
	var preprintErr error
	if len(notInPubMed) > 0 {
		preprints, preprintErr = c.Lookup.PreprintsByDOI(ctx, notInPubMed)
	}
	var arxiv []academic.Paper
	var arxivErr error
	if len(arxivIDs) > 0 {
		arxiv, arxivErr = c.Lookup.ArxivByID(ctx, arxivIDs)
	}

	for i, ref := range references {
		switch ref.Kind {
		case sourceidentity.PMID, sourceidentity.PMCID:
			if pubmedErr != nil {
				problems[i] = pubmedUnreachable
				continue
			}
			match, missing := func(p academic.Paper) bool { return p.PMID == ref.ID }, noSuchPMID
			if ref.Kind == sourceidentity.PMCID {
				match, missing = func(p academic.Paper) bool { return strings.EqualFold(p.PMCID, ref.ID) }, noSuchPMCID
			}
			if paper := paperBy(pubmed, match); paper != nil {
				found[i] = paperItem(*paper, sourcePubMed)
			} else {
				problems[i] = missing
			}
		case sourceidentity.DOI:
			byDOI := func(p academic.Paper) bool { return p.DOI == ref.ID }
			switch {
			case pubmedErr != nil:
				problems[i] = pubmedUnreachable
			case paperBy(pubmed, byDOI) != nil:
				found[i] = paperItem(*paperBy(pubmed, byDOI), sourcePubMed)
			case preprintErr != nil:
				problems[i] = preprintUnreachable
			case paperBy(preprints, byDOI) != nil:
				found[i] = paperItem(*paperBy(preprints, byDOI), sourcePreprint)
			default:
				found[i] = doiPage(ref.ID)
			}
		case sourceidentity.Arxiv:
			if arxivErr != nil {
				problems[i] = arxivUnreachable
				continue
			}
			byID := func(p academic.Paper) bool { return paperOf(p, sourceArxiv).ArxivID == ref.ID }
			if paper := paperBy(arxiv, byID); paper != nil {
				found[i] = paperItem(*paper, sourceArxiv)
			} else {
				problems[i] = noSuchArxiv
			}
		}
	}
	return found, problems
}

// paperBy is the first paper that matches, or nil.
func paperBy(papers []academic.Paper, match func(academic.Paper) bool) *academic.Paper {
	for i := range papers {
		if match(papers[i]) {
			return &papers[i]
		}
	}
	return nil
}

// paperItem is the item the research box imports for a paper its search
// found: the same address, title, snippet and paper ids (paperOf), so the
// two are one document.
func paperItem(found academic.Paper, source string) *item {
	address, text, ok := webAddress(found.URL)
	if !ok {
		return nil
	}
	return &item{
		address: address, url: text, title: clip(strings.TrimSpace(found.Title), maxTitleChars),
		snippet: strings.ReplaceAll(trim(found.Abstract, storedSnippetChars), "\x00", ""),
		source:  source, paper: paperOf(found, source),
	}
}

// doiPage is a DOI found in neither index: its publisher's page, which
// doi.org redirects to, titled with its address until the read says what
// it is called, and carrying the DOI so it is found again by it.
func doiPage(doi string) *item {
	text := "https://doi.org/" + doi
	address, _, ok := webAddress(text)
	paper, bad := Paper{DOI: doi}.checked()
	if !ok || bad != "" {
		return nil
	}
	return &item{address: address, url: text, title: clip(text, maxTitleChars), source: "web", paper: &paper}
}
