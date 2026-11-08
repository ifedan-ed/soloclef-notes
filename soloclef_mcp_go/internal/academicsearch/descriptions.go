package academicsearch

// The published descriptions of this slice's tools, taken from the Python
// server's docstrings unchanged. A client shows them to a model, so a reworded
// one is a different tool surface even where the behaviour is the same.

const PubmedSearchDescription = `Search PubMed (biomedical literature) by keyword.

        Returns, per paper: title, authors, year, journal, PMID/PMCID/DOI, the
        PubMed URL, and an abstract (or a full-text excerpt when
        full_text=True and the paper is in PMC's open-access subset).
        Example: query='bronchiolitis high flow nasal cannula', since=2018.
        `

const ArxivSearchDescription = `Search arXiv (preprints: physics, CS, math, stats, and more).

        Returns, per paper: title, authors, year, arXiv id, DOI/journal-ref
        when published, the arXiv URL, and the abstract.
        Example: query='transformer attention mechanism'.
        `

const PreprintSearchDescription = `Search medRxiv (health sciences) or bioRxiv (biology) preprints.

        These are NOT peer reviewed. Returns, per preprint: title, authors,
        year, DOI, the landing-page URL, and the abstract.
        Example: query='pediatric asthma exacerbation', server='medrxiv'.
        `

const ScholarSearchDescription = `Search Google Scholar across disciplines and publishers.

        Broadest coverage, but the least structured result (no reliable
        abstract or full text — only the search snippet). Prefer
        soloclef_pubmed_search, soloclef_arxiv_search or
        soloclef_preprint_search when the topic fits one of them.
        Returns, per hit: title, authors/venue/year line, URL, cited-by count
        when shown, and the snippet.
        Example: query='pediatric sepsis early recognition'.
        `
