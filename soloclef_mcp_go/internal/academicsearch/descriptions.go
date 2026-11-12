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
