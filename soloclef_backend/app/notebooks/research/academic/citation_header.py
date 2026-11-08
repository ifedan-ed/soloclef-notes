"""The citation block an imported paper opens with, so answers can cite it properly."""

from __future__ import annotations

from .paper import PaperRef

FULL_TEXT_NOTES = {
    "pmc_oa_xml": "PubMed Central open-access full text (XML)",
    "pdf": "open-access PDF",
    "preprint_jats_xml": "preprint full text (JATS XML)",
    "landing_page": "publisher page (read by the web crawler)",
    "abstract_only": "abstract only (no free full text found)",
}

_SOURCE_NAMES = {
    "pubmed": "PubMed",
    "arxiv": "arXiv",
    "medrxiv": "medRxiv / bioRxiv",
    "google_scholar": "Google Scholar",
}


def citation_header(
    title: str, paper: PaperRef, *, source: str, full_text: str, url: str
) -> str:
    facts = [
        f"Authors: {', '.join(paper.authors)}" if paper.authors else None,
        f"Published in: {paper.venue}" if paper.venue else None,
        f"Year: {paper.year}" if paper.year else None,
        f"DOI: {paper.doi}" if paper.doi else None,
        f"PMID: {paper.pmid}" if paper.pmid else None,
        f"PMCID: {paper.pmcid}" if paper.pmcid else None,
        f"arXiv: {paper.arxiv_id}" if paper.arxiv_id else None,
        f"Categories: {', '.join(paper.categories)}" if paper.categories else None,
        f"Found via: {_SOURCE_NAMES.get(source, source)}",
        f"Full text: {FULL_TEXT_NOTES.get(full_text, full_text)}",
        f"URL: {url}",
    ]
    lines = "\n".join(f"- {fact}" for fact in facts if fact)
    return f"# {title}\n\n{lines}"


def with_sections(header: str, *sections: tuple[str, str]) -> str:
    """The header followed by each non-empty ``(heading, text)`` section."""
    parts = [header]
    for heading, text in sections:
        if text and text.strip():
            parts.append(f"## {heading}\n\n{text.strip()}" if heading else text.strip())
    return "\n\n".join(parts)
